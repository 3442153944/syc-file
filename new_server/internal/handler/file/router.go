package file

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/internal/sync"
)

func RegisterFileRouter(rg *gin.RouterGroup, db *gorm.DB, redisClient *redis.Client, engine *sync.Engine) {
	f := rg.Group("/file")
	f.POST("/available-disks", HandlerFuncAvailableDisks(db, redisClient))
	f.POST("/traverse-directory", HandlerFuncTraverseDirectory(db, redisClient))
	f.GET("/download", HandlerFuncDownload(db, redisClient))
	f.POST("/upload", HandlerFuncUpload(db, redisClient))
	// 分片上传（重写版）
	f.POST("/upload/init", HandlerFuncUploadInit(db, redisClient))
	f.GET("/upload/status", HandlerFuncUploadStatus(db, redisClient))
	f.POST("/upload/chunk", HandlerFuncUploadChunk(db, redisClient))
	f.POST("/upload/complete", HandlerFuncUploadComplete(db, redisClient, engine))
	f.POST("/delete", HandlerFuncDeleteFile(db, redisClient))
	f.POST("/download-history", HandlerFuncDownloadHistory(db, redisClient))
	f.POST("/delete-download-history", DeleteDownloadHistory(db, redisClient))
}
