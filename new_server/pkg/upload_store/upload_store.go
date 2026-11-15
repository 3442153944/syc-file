// Package upload_store 管理分片上传会话在 Redis 中的状态：会话元数据 + 已收分片位图。
//
// 状态放 Redis（独立进程）而非 Go 进程内存,配合磁盘上的临时文件,可在服务重启后续传;
// Redis 若被清空则会话丢失,客户端重新 init 即整份重传——符合既定取舍。
//
// 会话 id 由 (userID, 目标路径, file_hash, total_size, chunk_size) 确定性派生:
// 客户端文件变了 → file_hash 变 → id 变 → 天然开新会话,不会误续到旧字节。
//
// 热路径约束（Rust receiver 接入后）: 分片接收/归档由进程内 Rust receiver 负责,
// Go 不再逐片读写 Redis。会话元数据读取因此增加进程内读穿缓存(短 TTL + 条目/字节上限),
// 使稳态连续分片期间 Redis 命令数降为 0;快照只在 receiver 的 snapshot_ready 时低频写入
// (见 PersistBitmap) 与 init/status/complete 等低频路径发生。
//
// 注意: 该缓存按【单 Go 进程、单实例】部署设计。多实例部署时各进程缓存互不可见,
// 需换回直接读 Redis 或做实例亲和,否则会读到过期元数据。
package upload_store

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	metaKeyPrefix = "upload:meta:" // Hash：会话元数据
	bitsKeyPrefix = "upload:bits:" // String：已收分片位图（SETBIT，MSB-first）
)

// 会话元数据进程内读穿缓存的取舍:
//   - TTL 5s 滑窗: 请求间隔小于 5s 的连续分片全部命中缓存(0 条 Redis 命令);
//     完成/删除/状态变更的可见延迟上限即 5s, 且这些路径都会主动失效缓存, 实际更短。
//   - 条目上限 4096: 覆盖单实例并发上传会话的常见规模, 超出逐出最旧(插入序)。
//   - 估算字节上限 64MiB: 与 Rust receiver 的 PENDING_CAP 同量级, 防止大量大会话元数据
//     (叶子哈希 hex 可以很长) 把 Go 进程内存撑爆; 超限也逐出最旧。
//   - 只缓存会话元数据, 绝不缓存位图(位图随分片变化, 必须由收片状态机/Redis 快照负责)。
const (
	metaCacheTTL        = 5 * time.Second
	metaCacheMaxEntries = 4096
	metaCacheMaxBytes   = 64 << 20
)

// ErrNotFound 表示会话不存在（或已过期）。
var ErrNotFound = errors.New("upload_store: 会话不存在")

// Session 一次分片上传会话的元数据。
type Session struct {
	ID         string `redis:"-"`
	UserID     uint   `redis:"user_id"`
	FileName   string `redis:"file_name"`
	TargetPath string `redis:"target_path"`
	TempPath   string `redis:"temp_path"`
	TotalSize  int64  `redis:"total_size"`
	ChunkSize  int64  `redis:"chunk_size"`
	ChunkCount int    `redis:"chunk_count"`
	MerkleRoot string `redis:"merkle_root"` // hex
	FileHash   string `redis:"file_hash"`   // hex，整文件 blake3
	Leaves     string `redis:"leaves"`      // hex，所有叶子哈希拼接（finalize 定位坏块用）
	Status     string `redis:"status"`      // uploading / completed
	HistoryID  uint64 `redis:"history_id"`
	CreatedAt  int64  `redis:"created_at"`
	// QuickShare 粘贴快传模式：complete 不建 file 记录/同步派发，直接生成分享记录。
	QuickShare bool   `redis:"quick_share"`
	ResultJSON string `redis:"result_json"`
}

// metaCacheEntry 一条缓存元数据。sess 按值保存, Get 返回副本, 避免调用方意外改写缓存。
type metaCacheEntry struct {
	key     string
	sess    Session
	size    int
	expires time.Time
	elem    *list.Element
}

type UploadStore struct {
	rdb *redis.Client

	mu         sync.Mutex
	cache      map[string]*metaCacheEntry
	order      *list.List // 插入序(FIFO), 用于超限逐出最旧
	cacheBytes int
	// epoch 全局失效代数: 任何元数据变更(Delete/SetStatus/Complete/Create)都会递增。
	// 用于封堵"Get 读到旧值 → 变更生效 → Get 回填缓存"的竞态: 回填前若代数已变则放弃缓存,
	// 否则会把变更前的旧元数据留在缓存里最多 metaCacheTTL。
	epoch uint64
}

var Global *UploadStore

func newUploadStore(rdb *redis.Client) *UploadStore {
	return &UploadStore{
		rdb:   rdb,
		cache: make(map[string]*metaCacheEntry),
		order: list.New(),
	}
}

// Init 初始化全局 store(测试/进程启动时调用)。缓存随新实例清空。
func Init(rdb *redis.Client) { Global = newUploadStore(rdb) }

func New(rdb *redis.Client) *UploadStore { return newUploadStore(rdb) }

// SessionID 由文件身份确定性派生会话 id，使 init 具备幂等/续传语义。
func SessionID(userID uint, targetPath, fileHash string, totalSize, chunkSize int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%d|%d", userID, targetPath, fileHash, totalSize, chunkSize)))
	return hex.EncodeToString(sum[:])
}

func metaKey(id string) string { return metaKeyPrefix + id }
func bitsKey(id string) string { return bitsKeyPrefix + id }

// ---- 元数据缓存（读穿 + 短 TTL + 容量上限） ----

// estimateSessionBytes 估算一条会话元数据在缓存里的内存占用（字符串内容 + 指针/结构开销）。
func estimateSessionBytes(s *Session) int {
	const perString = 40 // string header + map/list 记账等摊销
	n := 256 + perString*6
	n += len(s.ID) + len(s.FileName) + len(s.TargetPath) + len(s.TempPath)
	n += len(s.MerkleRoot) + len(s.FileHash) + len(s.Leaves) + len(s.Status) + len(s.ResultJSON)
	return n
}

func copySession(s *Session) *Session {
	cp := *s
	return &cp
}

// cacheGet 命中返回会话副本并刷新滑窗 TTL；未命中/已过期返回 nil。
func (s *UploadStore) cacheGet(id string) *Session {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.cache[id]
	if e == nil {
		return nil
	}
	if !now.Before(e.expires) {
		s.removeEntryLocked(e)
		return nil
	}
	e.expires = now.Add(metaCacheTTL)
	return copySession(&e.sess)
}

// cachePutIfEpoch 仅当失效代数未变时回填（封堵与并发变更的竞态）。
func (s *UploadStore) cachePutIfEpoch(sess *Session, epoch uint64) {
	if sess == nil || sess.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch != epoch {
		return
	}
	s.putLocked(sess)
}

// cacheReplace 写入/覆盖缓存条目并推进失效代数：比它更早开始的并发 Get 不会再把旧值回填。
func (s *UploadStore) cacheReplace(sess *Session) {
	if sess == nil || sess.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	s.putLocked(sess)
}

// putLocked 写入/覆盖缓存条目，并按条目数/字节数上限逐出最旧。调用方须持有 s.mu。
func (s *UploadStore) putLocked(sess *Session) {
	e := &metaCacheEntry{
		key:     sess.ID,
		sess:    *sess,
		size:    estimateSessionBytes(sess),
		expires: time.Now().Add(metaCacheTTL),
	}
	if old := s.cache[e.key]; old != nil {
		s.removeEntryLocked(old)
	}
	e.elem = s.order.PushBack(e)
	s.cache[e.key] = e
	s.cacheBytes += e.size
	for (len(s.cache) > metaCacheMaxEntries || s.cacheBytes > metaCacheMaxBytes) && s.order.Len() > 0 {
		s.removeEntryLocked(s.order.Front().Value.(*metaCacheEntry))
	}
}

// cacheEpoch 取当前失效代数（Get 回填前的竞态检查用）。
func (s *UploadStore) cacheEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// invalidate 主动失效一条缓存并推进失效代数（元数据被本进程修改后调用）。
func (s *UploadStore) invalidate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	if e := s.cache[id]; e != nil {
		s.removeEntryLocked(e)
	}
}

func (s *UploadStore) removeEntryLocked(e *metaCacheEntry) {
	delete(s.cache, e.key)
	if e.elem != nil {
		s.order.Remove(e.elem)
		e.elem = nil
	}
	s.cacheBytes -= e.size
	if s.cacheBytes < 0 {
		s.cacheBytes = 0
	}
}

// ---- 元数据读写 ----

// Create 写入会话元数据并设置 TTL（不预建位图，首个快照 PersistBitmap 时创建）。
func (s *UploadStore) Create(ctx context.Context, sess *Session, ttl time.Duration) error {
	m := map[string]interface{}{
		"user_id":     sess.UserID,
		"file_name":   sess.FileName,
		"target_path": sess.TargetPath,
		"temp_path":   sess.TempPath,
		"total_size":  sess.TotalSize,
		"chunk_size":  sess.ChunkSize,
		"chunk_count": sess.ChunkCount,
		"merkle_root": sess.MerkleRoot,
		"file_hash":   sess.FileHash,
		"leaves":      sess.Leaves,
		"status":      sess.Status,
		"history_id":  sess.HistoryID,
		"created_at":  sess.CreatedAt,
		"quick_share": sess.QuickShare,
	}
	if err := s.rdb.HSet(ctx, metaKey(sess.ID), m).Err(); err != nil {
		return err
	}
	if err := s.rdb.Expire(ctx, metaKey(sess.ID), ttl).Err(); err != nil {
		return err
	}
	s.cacheReplace(sess)
	return nil
}

// Get 读取会话；不存在返回 ErrNotFound。
// 先查进程内读穿缓存(命中刷新滑窗 TTL)，未命中 HGetAll 后回填。返回值是副本。
func (s *UploadStore) Get(ctx context.Context, id string) (*Session, error) {
	if sess := s.cacheGet(id); sess != nil {
		return sess, nil
	}
	epoch := s.cacheEpoch()
	m, err := s.rdb.HGetAll(ctx, metaKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, ErrNotFound
	}
	sess := sessionFromMap(id, m)
	s.cachePutIfEpoch(sess, epoch)
	return sess, nil
}

func sessionFromMap(id string, m map[string]string) *Session {
	atoi := func(k string) int64 { v, _ := strconv.ParseInt(m[k], 10, 64); return v }
	return &Session{
		ID:         id,
		UserID:     uint(atoi("user_id")),
		FileName:   m["file_name"],
		TargetPath: m["target_path"],
		TempPath:   m["temp_path"],
		TotalSize:  atoi("total_size"),
		ChunkSize:  atoi("chunk_size"),
		ChunkCount: int(atoi("chunk_count")),
		MerkleRoot: m["merkle_root"],
		FileHash:   m["file_hash"],
		Leaves:     m["leaves"],
		Status:     m["status"],
		HistoryID:  uint64(atoi("history_id")),
		CreatedAt:  atoi("created_at"),
		QuickShare: m["quick_share"] == "1",
		ResultJSON: m["result_json"],
	}
}

// Delete 删除会话元数据与位图（不动磁盘临时文件，由调用方决定）。
func (s *UploadStore) Delete(ctx context.Context, id string) error {
	err := s.rdb.Del(ctx, metaKey(id), bitsKey(id)).Err()
	if err == nil {
		s.invalidate(id)
	}
	return err
}

// SetStatus 更新会话状态。
func (s *UploadStore) SetStatus(ctx context.Context, id, status string) error {
	err := s.rdb.HSet(ctx, metaKey(id), "status", status).Err()
	if err == nil {
		s.invalidate(id)
	}
	return err
}

// MarkChunk 标记第 index 个分片已收（SETBIT）。保留给 init 的秒传预置位图等低频路径；
// 收片热路径已改由 Rust receiver 维护接收位图，不再逐片调用。
func (s *UploadStore) MarkChunk(ctx context.Context, id string, index int) error {
	return s.rdb.SetBit(ctx, bitsKey(id), int64(index), 1).Err()
}

// ClearChunk 清除第 index 个分片的已收标记（finalize 定位到坏块时用，令客户端重传该片）。
// 保留签名供其它调用点使用；Go 收片/完成路径已不再调用（坏块清位由 Rust receiver 负责）。
func (s *UploadStore) ClearChunk(ctx context.Context, id string, index int) error {
	return s.rdb.SetBit(ctx, bitsKey(id), int64(index), 0).Err()
}

// Bitmap 返回位图原始字节（未创建时返回 nil）。Redis SETBIT 为 MSB-first。
// 低频路径使用(init 续传/重启恢复/状态查询)，热路径不调用。
func (s *UploadStore) Bitmap(ctx context.Context, id string) ([]byte, error) {
	b, err := s.rdb.Get(ctx, bitsKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return b, err
}

// ReceivedCount 已收分片数（BITCOUNT）。低频路径使用。
func (s *UploadStore) ReceivedCount(ctx context.Context, id string) (int64, error) {
	return s.rdb.BitCount(ctx, bitsKey(id), nil).Result()
}

// Touch 刷新会话 TTL（元数据与位图一起续期）。低频路径使用；
// 收片热路径的续期已由 PersistBitmap 顺带完成（snapshot_ready 时）。
func (s *UploadStore) Touch(ctx context.Context, id string, ttl time.Duration) error {
	pipe := s.rdb.Pipeline()
	pipe.Expire(ctx, metaKey(id), ttl)
	pipe.Expire(ctx, bitsKey(id), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// PersistBitmap 把 receiver 导出的【已落盘】位图快照写回 Redis，并顺带续期元数据。
// 只有收到 receiver 的 snapshot_ready / complete 前才调用（低频）。
//
// pipeline: SET upload:bits:<id> bits + EXPIRE bits ttl + EXPIRE meta ttl。
// 元数据 key 已过期时 EXPIRE 返回 false 不算错误（会话正在被清理）。
func (s *UploadStore) PersistBitmap(ctx context.Context, id string, bits []byte, ttl time.Duration) error {
	if bits == nil {
		bits = []byte{} // SET 空位图 = 0 片落盘，语义与 nil 一致
	}
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, bitsKey(id), bits, 0)
	pipe.Expire(ctx, bitsKey(id), ttl)
	pipe.Expire(ctx, metaKey(id), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// MissingChunks 从位图字节里扫出仍缺失的分片索引（[0,chunkCount) 中未置位的）。
// 位序与 Redis SETBIT 一致：offset i 对应 byte i/8 的第 (7-i%8) 位（MSB-first）。
func MissingChunks(bitmap []byte, chunkCount int) []int {
	missing := make([]int, 0)
	for i := 0; i < chunkCount; i++ {
		byteIdx := i / 8
		set := false
		if byteIdx < len(bitmap) {
			set = (bitmap[byteIdx]>>(7-uint(i%8)))&1 == 1
		}
		if !set {
			missing = append(missing, i)
		}
	}
	return missing
}

// Complete 保留快传完成结果，完成响应丢失后可重取同一分享。TTL不超过分享有效期。
func (s *UploadStore) Complete(ctx context.Context, id, result string, ttl time.Duration) error {
	_, err := s.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, metaKey(id), "status", "completed", "result_json", result)
		pipe.Expire(ctx, metaKey(id), ttl)
		pipe.Del(ctx, bitsKey(id))
		return nil
	})
	if err == nil {
		s.invalidate(id)
	}
	return err
}
