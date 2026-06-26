package sync_store

import (
	"context"
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

func (s *SyncStore) AcquireFileLock(ctx context.Context, path string, ttl time.Duration) (bool, error) {
	ok, err := s.rdb.SetNX(ctx, fileLockKey(path), "1", ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (s *SyncStore) ReleaseFileLock(ctx context.Context, path string) error {
	return s.rdb.Del(ctx, fileLockKey(path)).Err()
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
