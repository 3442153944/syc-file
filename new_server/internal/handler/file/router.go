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
	f.POST("/upload/init", HandlerFuncUploadInit(db, redisClient, engine))
	f.GET("/upload/status", HandlerFuncUploadStatus(db, redisClient))
	f.POST("/upload/chunk", HandlerFuncUploadChunk(db, redisClient))
	f.POST("/upload/complete", HandlerFuncUploadComplete(db, redisClient, engine))
	f.POST("/delete", HandlerFuncDeleteFile(db, redisClient))
	f.POST("/download-history", HandlerFuncDownloadHistory(db, redisClient))
	// 版本历史（内容存版本仓库，见 internal/sync/version.go）
	f.POST("/versions", HandlerFuncFileVersions(db))
	f.GET("/version/download", HandlerFuncDownloadVersion(db))
	f.POST("/version/rollback", HandlerFuncRollbackVersion(db, engine))
	f.POST("/delete-download-history", DeleteDownloadHistory(db, redisClient))
	// 分享链接：硬链接到 temp 目录 + Redis + 独立生命周期协程
	f.POST("/share-link/create", HandlerFuncCreateShareLink(db, redisClient))
	// 分享链接下载（"阳"接口，见 config.yaml share 段落说明）：公开路由，在 whitelist 放行免登录
	f.GET("/share-link/download/:code", HandlerFuncShareDownload(db, redisClient))
	// 分享管理：仅创建者本人可查看/吊销自己的分享链接
	f.POST("/share-link/list", HandlerFuncShareLinkList(db))
	f.POST("/share-link/revoke", HandlerFuncShareLinkRevoke(db, redisClient))
}
