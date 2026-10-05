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
	// 图片缩略图：外网/流量下列表只拉几 KB，点进详情再拉原图（见 internal/thumb）
	f.GET("/thumbnail", HandlerFuncThumbnail(db, redisClient))
	// 文本文件在线查看/编辑：保存带版本号做乐观并发控制，多人同时编辑不会静默互相覆盖（见 text.go）
	f.GET("/text/read", HandlerFuncTextRead(db, redisClient))
	f.POST("/text/save", HandlerFuncTextSave(db, redisClient, engine))
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
	// 粘贴快传：单次原始字节上传到该用户专属目录，自动生成分享链接（见 quick_share.go）
	f.POST("/quick-share/upload", HandlerFuncQuickShareUpload(db, redisClient))
	f.POST("/quick-share/quota", HandlerFuncQuickShareQuota(db))
}
