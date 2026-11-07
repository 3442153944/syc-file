package file

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"syc-file/pkg/response"

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
//
// 热路径（Rust receiver 接入后）：Go 收到 HTTP 分片后只做参数/鉴权/会话/长度校验，
// 然后把分片整块交给进程内 Rust receiver（synccore.ReceiverAccept）——Go 不保存分片、
// 不做接收位图算法、不逐片访问 Redis。分片的缓存、落盘、abandoned 判断、长度校验、
// 重复判定全在 Rust；仅在 receiver 提示 snapshot_ready 时，Go 才把【已落盘】位图
// 低频写回 Redis（约 1s 一次），热路径稳态 0 条 Redis 命令。
//
// 注意：receiver 是进程内状态，服务端按单 Go 进程/单实例部署设计。
func HandlerFuncUploadChunk(db *gorm.DB, _ *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			response.WriteHTTP(c, http.StatusOK, 401, "请先登录", nil)
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		uploadID := firstNonEmpty(c.Query("upload_id"), c.GetHeader("Upload-Id"))
		indexStr := firstNonEmpty(c.Query("index"), c.GetHeader("Chunk-Index"))
		if uploadID == "" || indexStr == "" {
			response.WriteHTTP(c, http.StatusOK, 400, "缺少 upload_id 或 index", nil)
			return
		}
		attemptID := firstNonEmpty(c.Query("attempt_id"), c.GetHeader("Chunk-Attempt-Id"))
		if len(attemptID) > 128 {
			response.WriteHTTP(c, http.StatusOK, 400, "attempt_id 过长", nil)
			return
		}
		index, err := strconv.Atoi(indexStr)
		if err != nil || index < 0 {
			response.WriteHTTP(c, http.StatusOK, 400, "index 非法", nil)
			return
		}

		ctx := context.Background()
		sess, err := upload_store.Global.Get(ctx, uploadID)
		if errors.Is(err, upload_store.ErrNotFound) {
			// 会话不存在（过期/被清）→ 让客户端重新 init
			response.WriteHTTP(c, http.StatusOK, 404, "会话不存在，请重新初始化上传", nil)
			return
		}
		if err != nil {
			response.WriteHTTP(c, http.StatusOK, 500, "查询会话失败", nil)
			return
		}
		if sess.UserID != userID {
			response.WriteHTTP(c, http.StatusOK, 403, "无权访问该上传会话", nil)
			return
		}
		if !sess.QuickShare && !checkFilePath(c, db, sess.TargetPath) {
			return
		}
		if !checkUploadStage(c, db, sess) {
			return
		}
		if index >= sess.ChunkCount {
			response.WriteHTTP(c, http.StatusOK, 400, "index 越界", nil)
			return
		}

		// 读取分片 body，限制最大长度为 chunk_size，防止超大 body 打爆内存
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, sess.ChunkSize)
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			response.WriteHTTP(c, http.StatusOK, 400, "分片过大或读取失败", nil)
			return
		}

		// 校验分片长度：非末片须等于 chunk_size，末片等于剩余字节（Rust 侧还会再校验一次，
		// 这里保留是为了尽早拒绝/不把明显非法的 body 交给接收器）
		expectedLen := sess.ChunkSize
		if index == sess.ChunkCount-1 {
			expectedLen = sess.TotalSize - int64(index)*sess.ChunkSize
		}
		if int64(len(data)) != expectedLen {
			response.WriteHTTP(c, http.StatusOK, 400, "分片长度与描述不符", nil)
			return
		}

		// 请求体读取期间会话可被放弃/完成。锁内重查会话，再执行不可分割的接收提交。
		unlock := lockUploadCommit(uploadID)
		defer unlock()
		if c.Request.Context().Err() != nil {
			return
		}
		sess, err = upload_store.Global.Get(ctx, uploadID)
		if errors.Is(err, upload_store.ErrNotFound) {
			response.WriteHTTP(c, http.StatusOK, 404, "会话已结束", nil)
			return
		}
		if err != nil {
			response.WriteHTTP(c, http.StatusOK, 500, "查询会话失败", nil)
			return
		}

		// 已完成的会话只允许幂等确认，迟到分片不能写入已生成分享的文件。
		if sess.Status == "completed" {
			response.WriteHTTP(c, http.StatusOK, 409, "上传已完成，不接受迟到分片", nil)
			return
		}
		if !sess.QuickShare && !checkFilePath(c, db, sess.TargetPath) {
			return
		}

		// 1) 确保 Rust receiver 在线（进程重启后从 Redis persisted 位图恢复）
		if err := ensureReceiver(ctx, sess); err != nil {
			logger.Logger.Error("初始化分片接收器失败", zap.String("upload_id", uploadID), zap.Error(err))
			response.WriteHTTP(c, http.StatusOK, 500, "分片接收失败", nil)
			return
		}

		// 2) 整块交给 Rust：落 pending、幂等重复判定、abandoned 判定都在接收器内
		result, err := synccore.ReceiverAccept(uploadID, index, data, attemptID)
		if err != nil {
			switch {
			case errors.Is(err, synccore.ErrReceiverAbandoned):
				response.WriteHTTP(c, http.StatusOK, 409, "该分片尝试已放弃", nil)
			case errors.Is(err, synccore.ErrReceiverNotFound):
				// 锁内不应发生（ensureReceiver 刚检查过），记录以便排查
				logger.Logger.Error("分片接收器缺失", zap.String("upload_id", uploadID), zap.Int("index", index))
				response.WriteHTTP(c, http.StatusOK, 500, "分片写入失败", nil)
			default:
				logger.Logger.Error("分片接收失败", zap.String("upload_id", uploadID),
					zap.Int("index", index), zap.Error(err))
				response.WriteHTTP(c, http.StatusOK, 500, "分片写入失败", nil)
			}
			return
		}

		// 3) 幂等重复：此前已接受过该片，响应形状与旧实现一致
		if result.Duplicate {
			response.WriteHTTP(c, http.StatusOK, 200, "分片已确认", gin.H{"index": index})
			return
		}

		// 4) 低频快照：只有 receiver 认为到快照间隔了才写 Redis（约 1s 一次）。
		// 导出的是【已落盘】位图，不含仅内存 pending；成功后 ack 清 dirty，失败只 warn
		// （不 ack，下个间隔会重试）。
		if result.SnapshotReady {
			persistReceiverSnapshot(ctx, uploadID, sess.ChunkCount)
		}

		// 5) 原响应形状
		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"index": index, "received": result.Received, "chunk_count": sess.ChunkCount, "complete": result.Complete,
		})
	}
}

// ensureReceiver 确保 uploadID 对应的 Rust receiver 已注册（调用方须持有 uploadCommitLock）。
//
// 慢路径（首次分片 / 进程重启后首个请求）：从 Redis 的【persisted】位图恢复已收分片。
// 这样崩溃恢复只能恢复已落盘的片，与契约不变式 2 一致（内存里仅 accepted 的片绝不进快照，
// 重启后自然丢失，客户端按 missing 补传）。
func ensureReceiver(ctx context.Context, sess *upload_store.Session) error {
	if synccore.ReceiverExists(sess.ID) {
		return nil
	}
	bitmap, err := upload_store.Global.Bitmap(ctx, sess.ID)
	if err != nil {
		return err
	}
	bitmap = normalizePersistedBitmap(bitmap, sess.ChunkCount)
	_, err = synccore.ReceiverOpen(
		sess.ID, sess.TempPath,
		uint64(sess.TotalSize), uint64(sess.ChunkSize), sess.ChunkCount,
		bitmap, uploadSessionTTL,
	)
	return err
}

// normalizePersistedBitmap 把 Redis 里读到的位图规范成 Rust receiver 要求的精确长度
// need = ceil(chunkCount/8)。
//
// 兼容性（升级必修）：旧版本收片用逐片 SETBIT 维护 Redis 位图，Redis String 只在写到的
// 字节处增长、末尾字节不补齐——当 chunk_count>8 且最高已收索引 < (need-1)*8 时，位图长度
// 会小于 need。fc_receiver_open 的 Rust 侧解析要求长度精确等于 need，否则返回 FC_ERR_ARG，
// 会让切换部署时在途旧会话的 chunk/complete 持续 500（直到会话过期）。
// 这里只做长度补齐/截断：补 0 不改变旧位图已知位（未写到的片本就未收），截断丢弃多余字节；
// 位序（MSB-first）与每个 bit 的含义完全不变。len==0 保持 nil（等价「没有持久化位图」）。
func normalizePersistedBitmap(bitmap []byte, chunkCount int) []byte {
	need := (chunkCount + 7) / 8
	if need <= 0 || len(bitmap) == 0 {
		return nil
	}
	if len(bitmap) >= need {
		return bitmap[:need]
	}
	padded := make([]byte, need)
	copy(padded, bitmap)
	return padded
}

// persistReceiverSnapshot 把 receiver 的【已落盘】位图写入 Redis 快照，成功后 ack 清 dirty。
// 失败只记 warn（不 ack，下个快照间隔重试）——分片本身仍在 Rust/磁盘上，不影响正确性。
// 调用方须持有 uploadCommitLock，避免与 finalize/close 交错。
func persistReceiverSnapshot(ctx context.Context, uploadID string, chunkCount int) {
	bits, err := synccore.ReceiverBitmapPersisted(uploadID, chunkCount)
	if err != nil {
		logger.Logger.Warn("导出已落盘分片位图失败", zap.String("upload_id", uploadID), zap.Error(err))
		return
	}
	if err := upload_store.Global.PersistBitmap(ctx, uploadID, bits, uploadSessionTTL); err != nil {
		logger.Logger.Warn("持久化分片位图快照失败", zap.String("upload_id", uploadID), zap.Error(err))
		return
	}
	if err := synccore.ReceiverSnapshotAck(uploadID); err != nil {
		logger.Logger.Warn("确认分片位图快照失败", zap.String("upload_id", uploadID), zap.Error(err))
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
