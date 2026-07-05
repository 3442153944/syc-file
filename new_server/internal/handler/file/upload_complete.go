package file

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/internal/sync"
	"syc-file/pkg/filecore"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
)

// HandlerFuncUploadComplete 分片上传第三步：收齐后整体校验并原子落盘。
//
//	→ filecore.Finalize：单趟顺序读，逐块比对叶子 + 重建 Merkle 树根比对 + 算整文件哈希
//	→ filecore.Move：临时文件原子 rename 到目标
//	→ 写 file 表 / upload_history completed
//	→ 删 Redis 会话（临时文件已随 Move 消失）
func HandlerFuncUploadComplete(db *gorm.DB, _ *redis.Client, engine *sync.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		// 客户端统一发 JSON body（与 init 一致）；兼容 query/header 回退以防万一
		var req struct {
			UploadID string `json:"upload_id"`
			DeviceID string `json:"device_id"`
		}
		_ = c.ShouldBindJSON(&req)
		deviceID := firstNonEmpty(req.DeviceID, c.Query("device_id"), c.GetHeader("Device-Id"), c.PostForm("device_id"))
		uploadID := firstNonEmpty(req.UploadID, c.Query("upload_id"), c.GetHeader("Upload-Id"), c.PostForm("upload_id"))
		if uploadID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少 upload_id", "data": nil})
			return
		}

		ctx := context.Background()
		sess, err := upload_store.Global.Get(ctx, uploadID)
		if errors.Is(err, upload_store.ErrNotFound) {
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

		// 必须收齐所有分片
		bitmap, _ := upload_store.Global.Bitmap(ctx, uploadID)
		missing := upload_store.MissingChunks(bitmap, sess.ChunkCount)
		if len(missing) > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "分片未收齐", "data": gin.H{
				"missing": missing, "received": sess.ChunkCount - len(missing), "chunk_count": sess.ChunkCount,
			}})
			return
		}

		// 解码会话里的叶子哈希与树根，做整体校验
		leaves, err := hex.DecodeString(sess.Leaves)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "会话叶子哈希损坏", "data": nil})
			return
		}
		expectedRoot, err := hex.DecodeString(sess.MerkleRoot)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "会话树根损坏", "data": nil})
			return
		}

		fileHash, badIndex, err := filecore.Finalize(
			sess.TempPath, uint64(sess.ChunkSize), uint64(sess.TotalSize), leaves, expectedRoot,
		)
		if err != nil {
			switch {
			case errors.Is(err, filecore.ErrLeafMismatch) && badIndex >= 0:
				// 定位到坏块：清其置位，让客户端只重传该片
				_ = upload_store.Global.ClearChunk(ctx, uploadID, int(badIndex))
				logger.Logger.Warn("finalize 分片校验失败", zap.String("upload_id", uploadID), zap.Int64("bad_index", badIndex))
				c.JSON(http.StatusOK, gin.H{"code": 422, "message": "分片校验失败，请重传指定分片", "data": gin.H{
					"bad_index": badIndex,
				}})
			case errors.Is(err, filecore.ErrRootMismatch):
				// 树根对不上：按既定取舍让客户端整份重传
				logger.Logger.Warn("finalize 树根不一致", zap.String("upload_id", uploadID))
				c.JSON(http.StatusOK, gin.H{"code": 422, "message": "整体校验失败，请重新上传", "data": nil})
			default:
				logger.Logger.Error("finalize 失败", zap.String("upload_id", uploadID), zap.Error(err))
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "文件校验失败", "data": nil})
			}
			return
		}

		fileHashHex := hex.EncodeToString(fileHash)
		// 额外一致性：服务端算出的整文件哈希须与客户端描述一致
		if sess.FileHash != "" && fileHashHex != sess.FileHash {
			logger.Logger.Warn("整文件哈希与描述不一致", zap.String("upload_id", uploadID),
				zap.String("got", fileHashHex), zap.String("want", sess.FileHash))
			c.JSON(http.StatusOK, gin.H{"code": 422, "message": "整体校验失败，请重新上传", "data": nil})
			return
		}

		// 原子落盘
		if err := filecore.Move(sess.TempPath, sess.TargetPath); err != nil {
			logger.Logger.Error("落盘失败", zap.String("upload_id", uploadID), zap.String("dst", sess.TargetPath), zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "落盘失败", "data": nil})
			return
		}

		// 写 file 表：目标在同步文件夹内 → 交给 sync 引擎（维护 trunk + 向其它设备派发拉取）；
		// 否则按普通存储 upsert。二者只能其一，避免重复写 File 行。
		var fileID uint64
		var handled bool
		if engine != nil {
			var herr error
			handled, herr = engine.HandleUploadComplete(userID, deviceID, sess.TargetPath, sess.FileName, sess.TotalSize, fileHashHex)
			if herr != nil {
				logger.Logger.Warn("同步派发失败", zap.String("path", sess.TargetPath), zap.Error(herr))
			}
			if handled {
				fileID = lookupFileID(db, userID, sess.TargetPath)
			}
		}
		if !handled {
			id, ferr := upsertFileRecord(db, userID, sess.TargetPath, sess.FileName, sess.TotalSize, fileHashHex)
			if ferr != nil {
				logger.Logger.Error("写入文件记录失败", zap.String("path", sess.TargetPath), zap.Error(ferr))
			}
			fileID = id
		}
		if sess.HistoryID > 0 {
			now := time.Now()
			db.Model(&model.UploadHistory{}).Where("id = ?", sess.HistoryID).Updates(map[string]interface{}{
				"upload_status": "completed",
				"progress":      100,
				"completed_at":  &now,
			})
		}

		// 清理会话（临时文件已随 Move 消失）
		_ = upload_store.Global.Delete(ctx, uploadID)

		logger.Logger.Info("分片上传完成", zap.Uint("user_id", userID), zap.String("path", sess.TargetPath),
			zap.Int64("size", sess.TotalSize), zap.Uint64("file_id", fileID), zap.Bool("synced", handled))

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "上传成功", "data": gin.H{
			"file_id": fileID, "file_name": sess.FileName, "storage_path": sess.TargetPath,
			"file_size": sess.TotalSize, "file_hash": fileHashHex, "synced": handled,
		}})
	}
}

// lookupFileID 按 用户+路径 查 File 行 id（sync 引擎已建/更记录后取 id 用于响应）。
func lookupFileID(db *gorm.DB, userID uint, fullPath string) uint64 {
	var f model.File
	if err := db.Select("id").Where("user_id = ? AND file_path = ?", userID, fullPath).First(&f).Error; err != nil {
		return 0
	}
	return f.ID
}
