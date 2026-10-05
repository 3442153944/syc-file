package thumb

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// 对真实 Redis 验证到期索引的语义（有序集合的取到期/续期/删除）。
// 只在设置了 THUMB_REDIS_ADDR 时运行；用一次性的独立键，结束后删除，不碰线上的 thumb:tmp:exp。
func TestRedisIndexRoundTrip(t *testing.T) {
	addr := os.Getenv("THUMB_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 THUMB_REDIS_ADDR，跳过")
	}
	db, _ := strconv.Atoi(os.Getenv("THUMB_REDIS_DB"))
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("THUMB_REDIS_PASS"), DB: db})
	defer rdb.Close()

	ctx := context.Background()
	key := fmt.Sprintf("thumb:test:%d", time.Now().UnixNano())
	defer rdb.Del(ctx, key)
	idx := redisIndex{rdb: rdb, key: key}
	now := time.Now()

	for member, at := range map[string]time.Time{
		"aa/a_256.jpg": now.Add(-time.Minute),
		"bb/b_256.jpg": now.Add(time.Hour),
		"cc/c_256.jpg": now.Add(-2 * time.Minute),
	} {
		if err := idx.Touch(ctx, member, at); err != nil {
			t.Fatal(err)
		}
	}

	got, err := idx.Due(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cc/c_256.jpg", "aa/a_256.jpg"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Due 应按到期先后返回已到期的，期望 %v 实际 %v", want, got)
	}
	if got, _ := idx.Due(ctx, now, 1); len(got) != 1 {
		t.Errorf("limit=1 应只返回 1 个，实际 %v", got)
	}

	// 续期：a 被再次访问，到期时间顺延，就不该再出现在「已到期」里
	if err := idx.Touch(ctx, "aa/a_256.jpg", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := idx.Due(ctx, now, 10); !reflect.DeepEqual(got, []string{"cc/c_256.jpg"}) {
		t.Errorf("续期后只剩 c 到期，实际 %v", got)
	}

	if err := idx.Remove(ctx); err != nil {
		t.Errorf("Remove 不带成员应当无操作，实际 %v", err)
	}
	if err := idx.Remove(ctx, "cc/c_256.jpg"); err != nil {
		t.Fatal(err)
	}
	if got, _ := idx.Due(ctx, now, 10); len(got) != 0 {
		t.Errorf("删除后不该再有到期项，实际 %v", got)
	}
	if n, _ := rdb.ZCard(ctx, key).Result(); n != 2 {
		t.Errorf("索引里应剩 2 条，实际 %d", n)
	}
}

// 对真实 Redis 验证来源登记（HSET / HSCAN 分批 / HDEL）。同样只在设置 THUMB_REDIS_ADDR 时运行，用一次性独立键。
func TestRedisRegistryRoundTrip(t *testing.T) {
	addr := os.Getenv("THUMB_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 THUMB_REDIS_ADDR，跳过")
	}
	db, _ := strconv.Atoi(os.Getenv("THUMB_REDIS_DB"))
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("THUMB_REDIS_PASS"), DB: db})
	defer rdb.Close()

	ctx := context.Background()
	key := fmt.Sprintf("thumb:test-reg:%d", time.Now().UnixNano())
	defer rdb.Del(ctx, key)
	reg := redisRegistry{rdb: rdb, key: key}

	const n = 250
	for i := 0; i < n; i++ {
		if err := reg.Put(ctx, fmt.Sprintf("ab/k%03d_256.jpg", i), fmt.Sprintf("/data/src%03d.png", i)); err != nil {
			t.Fatal(err)
		}
	}
	// 覆盖写不应增加条目
	_ = reg.Put(ctx, "ab/k000_256.jpg", "/data/changed.png")

	got := map[string]string{}
	var cursor uint64
	for rounds := 0; rounds < 1000; rounds++ {
		entries, next, err := reg.Scan(ctx, cursor, 50)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range entries {
			got[k] = v
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	if len(got) != n {
		t.Fatalf("分批扫完应得到 %d 条，实际 %d", n, len(got))
	}
	if got["ab/k000_256.jpg"] != "/data/changed.png" || got["ab/k001_256.jpg"] != "/data/src001.png" {
		t.Errorf("条目内容不对: %v / %v", got["ab/k000_256.jpg"], got["ab/k001_256.jpg"])
	}

	if err := reg.Delete(ctx); err != nil {
		t.Errorf("Delete 不带成员应当无操作，实际 %v", err)
	}
	if err := reg.Delete(ctx, "ab/k000_256.jpg", "ab/k001_256.jpg"); err != nil {
		t.Fatal(err)
	}
	if c, _ := rdb.HLen(ctx, key).Result(); c != n-2 {
		t.Errorf("删除 2 条后应剩 %d，实际 %d", n-2, c)
	}
}
