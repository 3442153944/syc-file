// Package upload_store 管理分片上传会话在 Redis 中的状态：会话元数据 + 已收分片位图。
//
// 状态放 Redis（独立进程）而非 Go 进程内存,配合磁盘上的临时文件,可在服务重启后续传;
// Redis 若被清空则会话丢失,客户端重新 init 即整份重传——符合既定取舍。
//
// 会话 id 由 (userID, 目标路径, file_hash, total_size, chunk_size) 确定性派生:
// 客户端文件变了 → file_hash 变 → id 变 → 天然开新会话,不会误续到旧字节。
package upload_store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	metaKeyPrefix = "upload:meta:" // Hash：会话元数据
	bitsKeyPrefix = "upload:bits:" // String：已收分片位图（SETBIT，MSB-first）
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
}

type UploadStore struct {
	rdb *redis.Client
}

var Global *UploadStore

func Init(rdb *redis.Client) { Global = &UploadStore{rdb: rdb} }

func New(rdb *redis.Client) *UploadStore { return &UploadStore{rdb: rdb} }

// SessionID 由文件身份确定性派生会话 id，使 init 具备幂等/续传语义。
func SessionID(userID uint, targetPath, fileHash string, totalSize, chunkSize int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%d|%d", userID, targetPath, fileHash, totalSize, chunkSize)))
	return hex.EncodeToString(sum[:])
}

func metaKey(id string) string { return metaKeyPrefix + id }
func bitsKey(id string) string { return bitsKeyPrefix + id }

// Create 写入会话元数据并设置 TTL（不预建位图，首个分片 SETBIT 时惰性创建）。
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
	}
	if err := s.rdb.HSet(ctx, metaKey(sess.ID), m).Err(); err != nil {
		return err
	}
	return s.rdb.Expire(ctx, metaKey(sess.ID), ttl).Err()
}

// Get 读取会话；不存在返回 ErrNotFound。
func (s *UploadStore) Get(ctx context.Context, id string) (*Session, error) {
	m, err := s.rdb.HGetAll(ctx, metaKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, ErrNotFound
	}
	atoi := func(k string) int64 { v, _ := strconv.ParseInt(m[k], 10, 64); return v }
	sess := &Session{
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
	}
	return sess, nil
}

// Delete 删除会话元数据与位图（不动磁盘临时文件，由调用方决定）。
func (s *UploadStore) Delete(ctx context.Context, id string) error {
	return s.rdb.Del(ctx, metaKey(id), bitsKey(id)).Err()
}

// SetStatus 更新会话状态。
func (s *UploadStore) SetStatus(ctx context.Context, id, status string) error {
	return s.rdb.HSet(ctx, metaKey(id), "status", status).Err()
}

// MarkChunk 标记第 index 个分片已收（SETBIT）。
func (s *UploadStore) MarkChunk(ctx context.Context, id string, index int) error {
	return s.rdb.SetBit(ctx, bitsKey(id), int64(index), 1).Err()
}

// ClearChunk 清除第 index 个分片的已收标记（finalize 定位到坏块时用，令客户端重传该片）。
func (s *UploadStore) ClearChunk(ctx context.Context, id string, index int) error {
	return s.rdb.SetBit(ctx, bitsKey(id), int64(index), 0).Err()
}

// Bitmap 返回位图原始字节（未创建时返回 nil）。Redis SETBIT 为 MSB-first。
func (s *UploadStore) Bitmap(ctx context.Context, id string) ([]byte, error) {
	b, err := s.rdb.Get(ctx, bitsKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return b, err
}

// ReceivedCount 已收分片数（BITCOUNT）。
func (s *UploadStore) ReceivedCount(ctx context.Context, id string) (int64, error) {
	return s.rdb.BitCount(ctx, bitsKey(id), nil).Result()
}

// Touch 刷新会话 TTL（元数据与位图一起续期）。
func (s *UploadStore) Touch(ctx context.Context, id string, ttl time.Duration) error {
	pipe := s.rdb.Pipeline()
	pipe.Expire(ctx, metaKey(id), ttl)
	pipe.Expire(ctx, bitsKey(id), ttl)
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
