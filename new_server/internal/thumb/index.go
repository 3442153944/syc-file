package thumb

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Index 临时缩略图的到期索引：记录「哪张缩略图在什么时间到期」，供后台定时销毁。
// 抽成接口是为了让清理逻辑不依赖具体的 Redis，测试里用内存实现。
type Index interface {
	// Touch 记录（或续期）member 的到期时间；member 已存在则覆盖旧的到期时间。
	Touch(ctx context.Context, member string, expireAt time.Time) error
	// Due 取出到期时间不晚于 now 的 member，最多 limit 个。
	Due(ctx context.Context, now time.Time, limit int) ([]string, error)
	// Remove 删除索引条目。
	Remove(ctx context.Context, members ...string) error
}

// redisIndexKey 临时缩略图到期索引：有序集合，成员是缩略图相对临时目录的路径，分数是到期的 Unix 秒。
const redisIndexKey = "thumb:tmp:exp"

type redisIndex struct {
	rdb *redis.Client
	key string // 到期索引的键；生产用 redisIndexKey，测试用独立的键，不碰线上数据
}

// NewRedisIndex 用 Redis 有序集合实现 Index。
func NewRedisIndex(rdb *redis.Client) Index { return redisIndex{rdb: rdb, key: redisIndexKey} }

func (r redisIndex) Touch(ctx context.Context, member string, expireAt time.Time) error {
	return r.rdb.ZAdd(ctx, r.key, redis.Z{Score: float64(expireAt.Unix()), Member: member}).Err()
}

func (r redisIndex) Due(ctx context.Context, now time.Time, limit int) ([]string, error) {
	return r.rdb.ZRangeByScore(ctx, r.key, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(now.Unix(), 10),
		Offset: 0,
		Count:  int64(limit),
	}).Result()
}

func (r redisIndex) Remove(ctx context.Context, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	args := make([]interface{}, len(members))
	for i, m := range members {
		args[i] = m
	}
	return r.rdb.ZRem(ctx, r.key, args...).Err()
}

// Registry 持久缩略图的「来源登记」：缩略图文件 → 它是哪个源文件生成的。
// 持久缩略图的缓存键含源文件的修改时间，源文件被改动/删除后旧缩略图就成了没人引用的孤儿，
// 而从缩略图文件名反推不出源路径，所以生成时登记下来，后台定时按登记表清理。
type Registry interface {
	// Put 登记（或覆盖）member 对应的源文件路径。
	Put(ctx context.Context, member, src string) error
	// Scan 分批取出登记项：cursor 从 0 开始，返回的 next 为 0 表示扫完。
	Scan(ctx context.Context, cursor uint64, count int) (entries map[string]string, next uint64, err error)
	// Delete 删除登记项。
	Delete(ctx context.Context, members ...string) error
}

// redisRegistryKey 持久缩略图来源登记：哈希表，字段是缩略图相对持久目录的路径，值是源文件绝对路径。
const redisRegistryKey = "thumb:perm:src"

type redisRegistry struct {
	rdb *redis.Client
	key string
}

// NewRedisRegistry 用 Redis 哈希表实现 Registry。
func NewRedisRegistry(rdb *redis.Client) Registry {
	return redisRegistry{rdb: rdb, key: redisRegistryKey}
}

func (r redisRegistry) Put(ctx context.Context, member, src string) error {
	return r.rdb.HSet(ctx, r.key, member, src).Err()
}

func (r redisRegistry) Scan(ctx context.Context, cursor uint64, count int) (map[string]string, uint64, error) {
	// HSCAN 返回的是 [字段, 值, 字段, 值, ...] 的平铺列表
	kv, next, err := r.rdb.HScan(ctx, r.key, cursor, "", int64(count)).Result()
	if err != nil {
		return nil, 0, err
	}
	out := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out, next, nil
}

func (r redisRegistry) Delete(ctx context.Context, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	return r.rdb.HDel(ctx, r.key, members...).Err()
}
