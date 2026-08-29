package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"net/http"
	"syc-file/internal/admin"
	"syc-file/internal/clipboard"
	"syc-file/internal/handler/file"
	"syc-file/internal/handler/user"
	"syc-file/internal/middleware"
	"syc-file/internal/sync"
	"syc-file/internal/update"
	"syc-file/internal/ws"
)

func RegisterRouters(r *gin.Engine, db *gorm.DB, redisClient *redis.Client, engine *sync.Engine) {
	v1 := r.Group("/v1")
	// 统一认证中间件：是否需要登录由配置文件 whitelist 决定，无需再手动区分路由组
	v1.Use(middleware.Auth())

	v1.POST("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	// 所有业务路由统一注册，白名单外的路由默认需要登录
	user.RegisterUserRouter(v1, db, redisClient)
	file.RegisterFileRouter(v1, db, redisClient, engine)
	v1.POST("/user/update-info", user.HandlerFuncUpdateUserInfo(db, redisClient))
	v1.POST("/user/change-password", user.HandlerFuncChangePassword(db, redisClient))
	ws.RegisterWSRouter(v1, db, redisClient)
	sync.RegisterSyncRouter(v1, engine)
	update.RegisterUpdateRouter(v1, db)
	// 管理域：用户/设备/操作日志/存储配额/角色权限/粘贴快传缓存 + 系统监控
	admin.RegisterAdminRouter(v1, db, redisClient)
	// 剪贴板同步（纯转发 + Redis 短期历史，不落 MySQL）
	clipboard.RegisterClipboardRouter(v1, redisClient)
	// 注：/monitor 的 HTTP 路由在 admin.RegisterAdminRouter 里注册，此处不要重复挂
	// （重复注册同一路径 gin 会直接 panic）。实时刷新走 WS 推送（monitor/broadcaster.go）。
}
