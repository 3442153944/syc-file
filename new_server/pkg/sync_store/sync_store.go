package sync_store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	queueKeyPrefix    = "sync:queue"
	fileLockKeyPrefix = "sync:lock:file:"
	progressKeyPrefix = "sync:progress:"
	pendingKeyPrefix  = "sync:pending:user:"
)

type SyncStore struct {
	rdb *redis.Client
}

var Global *SyncStore

func Init(rdb *redis.Client) {
	Global = &SyncStore{rdb: rdb}
}

func New(rdb *redis.Client) *SyncStore {
	return &SyncStore{rdb: rdb}
}

func (s *SyncStore) EnqueueTask(ctx context.Context, taskID uint64) error {
	return s.rdb.LPush(ctx, queueKeyPrefix, strconv.FormatUint(taskID, 10)).Err()
}

func (s *SyncStore) DequeueTask(ctx context.Context, timeout time.Duration) (uint64, error) {
	res, err := s.rdb.BRPop(ctx, timeout, queueKeyPrefix).Result()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	if len(res) < 2 {
		return 0, nil
	}
	id, err := strconv.ParseUint(res[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// releaseLockScript 仅当锁的值等于自己持有的令牌时才删除，避免误删他人重新获取的锁。
var releaseLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end`)

// AcquireFileLock 以唯一令牌抢占文件锁（SetNX）。key 为逻辑键（如 "user:folder:relpath"）。
// 返回的 token 需随任务持久化，释放/续租时凭令牌操作。
func (s *SyncStore) AcquireFileLock(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	token, err := newLockToken()
	if err != nil {
		return "", false, err
	}
	ok, err := s.rdb.SetNX(ctx, fileLockKey(key), token, ttl).Result()
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return token, true, nil
}

// ReleaseFileLock 凭令牌安全释放文件锁；token 为空或不匹配时不做任何删除。
func (s *SyncStore) ReleaseFileLock(ctx context.Context, key, token string) error {
	if token == "" {
		return nil
	}
	return releaseLockScript.Run(ctx, s.rdb, []string{fileLockKey(key)}, token).Err()
}

// RenewFileLock 凭令牌续租文件锁 TTL（长任务防止锁中途过期）。
func (s *SyncStore) RenewFileLock(ctx context.Context, key, token string, ttl time.Duration) error {
	if token == "" {
		return nil
	}
	return renewLockScript.Run(ctx, s.rdb, []string{fileLockKey(key)}, token, int(ttl.Seconds())).Err()
}

// renewLockScript 仅当令牌匹配时才续租，避免续到他人的锁。
var renewLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("expire", KEYS[1], ARGV[2])
else
	return 0
end`)

func newLockToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SyncStore) UpdateProgress(ctx context.Context, taskID uint64, progress int, bytesTransferred int64) error {
	key := progressKey(taskID)
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, "progress", progress, "bytes", bytesTransferred, "updated_at", time.Now().Unix())
	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *SyncStore) GetProgress(ctx context.Context, taskID uint64) (int, int64, error) {
	key := progressKey(taskID)
	res, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return 0, 0, err
	}
	if len(res) == 0 {
		return 0, 0, nil
	}
	progress, _ := strconv.Atoi(res["progress"])
	bytes, _ := strconv.ParseInt(res["bytes"], 10, 64)
	return progress, bytes, nil
}

func (s *SyncStore) ResetProgress(ctx context.Context, taskID uint64) error {
	return s.rdb.Del(ctx, progressKey(taskID)).Err()
}

func (s *SyncStore) IncPending(ctx context.Context, userID uint) error {
	return s.rdb.Incr(ctx, pendingKey(userID)).Err()
}

func (s *SyncStore) DecPending(ctx context.Context, userID uint) error {
	n, err := s.rdb.Decr(ctx, pendingKey(userID)).Result()
	if err != nil {
		return err
	}
	if n < 0 {
		s.rdb.Set(ctx, pendingKey(userID), 0, 0)
	}
	return nil
}

func (s *SyncStore) GetPending(ctx context.Context, userID uint) (int64, error) {
	n, err := s.rdb.Get(ctx, pendingKey(userID)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return n, err
}

func fileLockKey(path string) string {
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%s%s", fileLockKeyPrefix, hex.EncodeToString(sum[:16]))
}

func progressKey(taskID uint64) string {
	return fmt.Sprintf("%s%d", progressKeyPrefix, taskID)
}

func pendingKey(userID uint) string {
	return fmt.Sprintf("%s%d", pendingKeyPrefix, userID)
}
