package file

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/pkg/logger"
	"syc-file/pkg/synccore"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
)

// HandlerFuncUploadChunk 分片上传第二步：接收单个分片（乱序、可并发、可重传）。
//
// 传参：upload_id 与 index 走 query 或 header，分片二进制走请求 body（raw）。
// 落盘：synccore.ChunkWrite 先用会话里该片的叶子哈希做 blake3 早校验，通过后按
// offset = index*chunk_size 定位写；校验不过则拒绝且不置位，客户端重传该片即可。
func HandlerFuncUploadChunk(db *gorm.DB, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		uploadID := firstNonEmpty(c.Query("upload_id"), c.GetHeader("Upload-Id"))
		indexStr := firstNonEmpty(c.Query("index"), c.GetHeader("Chunk-Index"))
		if uploadID == "" || indexStr == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少 upload_id 或 index", "data": nil})
			return
		}
		attemptID := firstNonEmpty(c.Query("attempt_id"), c.GetHeader("Chunk-Attempt-Id"))
		if len(attemptID) > 128 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "attempt_id 过长"})
			return
		}
		index, err := strconv.Atoi(indexStr)
		if err != nil || index < 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "index 非法", "data": nil})
			return
		}

		ctx := context.Background()
		sess, err := upload_store.Global.Get(ctx, uploadID)
		if errors.Is(err, upload_store.ErrNotFound) {
			// 会话不存在（过期/被清）→ 让客户端重新 init
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "会话不存在，请重新初始化上传", "data": nil})
			return
		}
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询会话失败", "data": nil})
			return
		}
		if sess.UserID != userID {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权访问该上传会话", "data": nil})
			return
		}
		if index >= sess.ChunkCount {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "index 越界", "data": nil})
			return
		}

		// 读取分片 body，限制最大长度为 chunk_size，防止超大 body 打爆内存
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, sess.ChunkSize)
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "分片过大或读取失败", "data": nil})
			return
		}

		// 校验分片长度：非末片须等于 chunk_size，末片等于剩余字节
		expectedLen := sess.ChunkSize
		if index == sess.ChunkCount-1 {
			expectedLen = sess.TotalSize - int64(index)*sess.ChunkSize
		}
		if int64(len(data)) != expectedLen {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "分片长度与描述不符", "data": nil})
			return
		}

		// 取该片的期望叶子哈希（会话里存的是全部叶子的 hex 拼接）
		leafHexStart := index * synccore.HashSize * 2
		leafHexEnd := leafHexStart + synccore.HashSize*2
		if leafHexEnd > len(sess.Leaves) {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "会话叶子哈希缺失", "data": nil})
			return
		}
		expectedLeaf, err := hex.DecodeString(sess.Leaves[leafHexStart:leafHexEnd])
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "会话叶子哈希损坏", "data": nil})
			return
		}

		// 请求体读取期间可被放弃。锁内重查会话/尝试/位图，再执行不可分割的写入提交。
		unlock := lockUploadCommit(uploadID)
		defer unlock()
		if c.Request.Context().Err() != nil {
			return
		}
		sess, err = upload_store.Global.Get(ctx, uploadID)
		if errors.Is(err, upload_store.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "会话已结束"})
			return
		}
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询会话失败"})
			return
		}
		if attemptID != "" {
			abandoned, err := rdb.HExists(c.Request.Context(), "upload:abandoned:"+uploadID, strconv.Itoa(index)+":"+attemptID).Result()
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "检查分片尝试失败"})
				return
			}
			if abandoned {
				c.JSON(http.StatusOK, gin.H{"code": 409, "message": "该分片尝试已放弃"})
				return
			}
		}
		bitmap, err := upload_store.Global.Bitmap(ctx, uploadID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询分片状态失败"})
			return
		}
		if index/8 < len(bitmap) && (bitmap[index/8]>>(7-uint(index%8)))&1 == 1 {
			c.JSON(http.StatusOK, gin.H{"code": 200, "message": "分片已确认", "data": gin.H{"index": index}})
			return
		}
		offset := uint64(index) * uint64(sess.ChunkSize)
		if err := synccore.ChunkWrite(sess.TempPath, offset, data, expectedLeaf); err != nil {
			switch {
			case errors.Is(err, synccore.ErrLeafMismatch):
				logger.Logger.Warn("分片校验失败", zap.String("upload_id", uploadID), zap.Int("index", index))
				c.JSON(http.StatusOK, gin.H{"code": 422, "message": "分片校验失败，请重传该分片", "data": gin.H{"index": index}})
			default:
				logger.Logger.Error("分片写入失败", zap.String("upload_id", uploadID), zap.Int("index", index), zap.Error(err))
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "分片写入失败", "data": nil})
			}
			return
		}

		// 置位 + 续期
		if err := upload_store.Global.MarkChunk(ctx, uploadID, index); err != nil {
			logger.Logger.Error("标记分片失败", zap.String("upload_id", uploadID), zap.Int("index", index), zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "标记分片失败", "data": nil})
			return
		}
		_ = upload_store.Global.Touch(ctx, uploadID, uploadSessionTTL)

		received, _ := upload_store.Global.ReceivedCount(ctx, uploadID)
		complete := int(received) >= sess.ChunkCount
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
			"index": index, "received": received, "chunk_count": sess.ChunkCount, "complete": complete,
		}})
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
