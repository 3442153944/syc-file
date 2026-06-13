package file

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func RegisterFileRouter(rg *gin.RouterGroup, db *gorm.DB, redisClient *redis.Client) {
	f := rg.Group("/file")
	f.POST("/available-disks", HandlerFuncAvailableDisks(db, redisClient))
	f.POST("/traverse-directory", HandlerFuncTraverseDirectory(db, redisClient))
	f.GET("/download", HandlerFuncDownload(db, redisClient))
	f.POST("/upload", HandlerFuncUpload(db, redisClient))
	f.POST("/download-history", HandlerFuncDownloadHistory(db, redisClient))
	f.POST("/delete-download-history", DeleteDownloadHistory(db, redisClient))
}
