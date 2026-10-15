package file

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
)

// 所有入口代理到同一写入后端。读取 body 时不占锁；提交、放弃和 complete
// 共用会话锁，避免迟到写入与 Finalize/Move 交错。引用归零即回收锁。
var uploadCommitLocks = struct {
	sync.Mutex
	entries map[string]*uploadCommitLock
}{entries: make(map[string]*uploadCommitLock)}

type uploadCommitLock struct {
	mu   sync.Mutex
	refs int
}

func lockUploadCommit(id string) func() {
	uploadCommitLocks.Lock()
	entry := uploadCommitLocks.entries[id]
	if entry == nil {
		entry = &uploadCommitLock{}
		uploadCommitLocks.entries[id] = entry
	}
	entry.refs++
	uploadCommitLocks.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		uploadCommitLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(uploadCommitLocks.entries, id)
		}
		uploadCommitLocks.Unlock()
	}
}

type abandonedChunkAttempt struct {
	Index     int    `json:"index"`
	AttemptID string `json:"attempt_id"`
}

// 放弃的是一次分片尝试，不能清除已确认位图，也不能取消同一分片的新尝试。
func HandlerFuncUploadAbandon(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录"})
			return
		}
		var req struct {
			UploadID string                  `json:"upload_id"`
			Attempts []abandonedChunkAttempt `json:"attempts"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		if c.ShouldBindJSON(&req) != nil || req.UploadID == "" || len(req.Attempts) == 0 || len(req.Attempts) > 128 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "放弃参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		unlock := lockUploadCommit(req.UploadID)
		defer unlock()
		sess, err := upload_store.Global.Get(ctx, req.UploadID)
		if errors.Is(err, upload_store.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "会话不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询会话失败"})
			return
		}
		if sess.UserID != uint(claims.(*token.Claims).UserID) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权访问该上传会话"})
			return
		}
		fields := make(map[string]interface{}, len(req.Attempts))
		for _, a := range req.Attempts {
			if a.Index < 0 || a.Index >= sess.ChunkCount || len(a.AttemptID) == 0 || len(a.AttemptID) > 128 {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "分片尝试无效"})
				return
			}
			fields[strconv.Itoa(a.Index)+":"+a.AttemptID] = "1"
		}
		key := "upload:abandoned:" + req.UploadID
		pipe := rdb.TxPipeline()
		pipe.HSet(ctx, key, fields)
		pipe.Expire(ctx, key, uploadSessionTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "记录放弃失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "已放弃分片尝试", "data": gin.H{"count": len(req.Attempts)}})
	}
}
