package upload_store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*UploadStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb), mr
}

func testSession(id string) *Session {
	return &Session{
		ID: id, UserID: 7, FileName: "a.bin", TargetPath: "/d/a.bin", TempPath: "/tmp/" + id + ".part",
		TotalSize: 10, ChunkSize: 4, ChunkCount: 3, Status: "uploading",
		MerkleRoot: "root", FileHash: "hash", Leaves: "leaves", CreatedAt: 1,
	}
}

// TestGetReadThroughCache 验证 Get 读写穿缓存：Create 后命中缓存（Redis 数据被删也能读到），
// 主动失效后回落 Redis（此时读到 ErrNotFound）。
func TestGetReadThroughCache(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	if err := st.Create(ctx, testSession("id1"), time.Hour); err != nil {
		t.Fatal(err)
	}
	// 直接删掉 Redis 元数据：缓存仍应命中（说明 Get 没访问 Redis）
	if err := st.rdb.Del(ctx, metaKey("id1")).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, "id1")
	if err != nil || got.FileName != "a.bin" || got.ChunkCount != 3 {
		t.Fatalf("缓存命中失败: err=%v sess=%+v", err, got)
	}
	// 返回副本：调用方改字段不得污染缓存
	got.FileName = "mutated"
	again, _ := st.Get(ctx, "id1")
	if again.FileName != "a.bin" {
		t.Fatalf("Get 返回了缓存内部指针（被调用方改写）")
	}
	// 失效后回落 Redis → 已删 → ErrNotFound
	st.invalidate("id1")
	if _, err := st.Get(ctx, "id1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("失效后应回落到 Redis 并返回 ErrNotFound, got %v", err)
	}
}

// TestGetCacheExpires 滑窗 TTL 过期后必须重新读 Redis（未命中回填）。
func TestGetCacheExpires(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	if err := st.Create(ctx, testSession("id-exp"), time.Hour); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.cache["id-exp"].expires = time.Now().Add(-time.Second)
	st.mu.Unlock()
	if err := st.rdb.Del(ctx, metaKey("id-exp")).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "id-exp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("缓存过期后应回落 Redis 并返回 ErrNotFound, got %v", err)
	}
}

// TestCacheEntryCap 条目上限必须逐出最旧（FIFO），不得无限增长。
func TestCacheEntryCap(t *testing.T) {
	st, _ := newTestStore(t)
	for i := 0; i < metaCacheMaxEntries+100; i++ {
		st.cacheReplace(testSession(fmt.Sprintf("cap-%d", i)))
	}
	st.mu.Lock()
	n, bytesLen := len(st.cache), st.cacheBytes
	st.mu.Unlock()
	if n > metaCacheMaxEntries || bytesLen > metaCacheMaxBytes {
		t.Fatalf("缓存超限: entries=%d bytes=%d", n, bytesLen)
	}
	// 最旧的一批应已被逐出
	if st.cacheGet("cap-0") != nil {
		t.Fatalf("最旧条目未被逐出")
	}
	if st.cacheGet("cap-"+fmt.Sprint(metaCacheMaxEntries+99)) == nil {
		t.Fatalf("最新条目应仍在缓存")
	}
}

// TestPersistBitmap 验证快照协议：SET 位图 + 位图/元数据 TTL 续期，且不破坏元数据缓存。
func TestPersistBitmap(t *testing.T) {
	st, mr := newTestStore(t)
	ctx := context.Background()
	sess := testSession("id-bits")
	sess.Leaves = ""
	if err := st.Create(ctx, sess, time.Hour); err != nil {
		t.Fatal(err)
	}
	bits := []byte{0x80, 0x01}
	if err := st.PersistBitmap(ctx, "id-bits", bits, 30*time.Minute); err != nil {
		t.Fatalf("PersistBitmap: %v", err)
	}
	raw, err := st.rdb.Get(ctx, bitsKey("id-bits")).Bytes()
	if err != nil || !bytes.Equal(raw, bits) {
		t.Fatalf("位图未写入: err=%v raw=%x", err, raw)
	}
	if ttl := mr.TTL(bitsKey("id-bits")); ttl <= 0 || ttl > 30*time.Minute {
		t.Fatalf("位图 TTL 异常: %v", ttl)
	}
	if ttl := mr.TTL(metaKey("id-bits")); ttl <= 0 || ttl > 30*time.Minute {
		t.Fatalf("元数据 TTL 未续期: %v", ttl)
	}
	// 快照只写 Redis 位图，不影响元数据缓存/内容
	got, err := st.Get(ctx, "id-bits")
	if err != nil || got.ID != "id-bits" {
		t.Fatalf("快照后读会话失败: %v", err)
	}
}

// TestMissingChunks 校验位序与 Redis SETBIT 一致（MSB-first）。
func TestMissingChunks(t *testing.T) {
	cases := []struct {
		name       string
		bitmap     []byte
		chunkCount int
		want       []int
	}{
		{"nil位图全缺", nil, 3, []int{0, 1, 2}},
		{"首片已收", []byte{0x80}, 8, []int{1, 2, 3, 4, 5, 6, 7}}, // offset0 = 最高位
		{"全收", []byte{0xFF}, 8, []int{}},
		{"隔位收", []byte{0xA0}, 4, []int{1, 3}},                         // 1010 0000 → 收到 0、2
		{"跨字节", []byte{0x00, 0x80}, 9, []int{0, 1, 2, 3, 4, 5, 6, 7}}, // 第8片(byte1最高位)已收
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MissingChunks(tc.bitmap, tc.chunkCount)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MissingChunks = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

// TestSessionIDDeterministic 相同身份必得相同 id，文件哈希变则 id 变。
func TestSessionIDDeterministic(t *testing.T) {
	a := SessionID(1, "E:\\a\\b.bin", "abcd", 100, 10)
	b := SessionID(1, "E:\\a\\b.bin", "abcd", 100, 10)
	if a != b {
		t.Fatalf("同身份 id 不一致: %s vs %s", a, b)
	}
	c := SessionID(1, "E:\\a\\b.bin", "ef01", 100, 10) // file_hash 变
	if a == c {
		t.Fatalf("文件哈希变化后 id 应不同")
	}
}
