package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"net/http"
	"syc-file/internal/handler/file"
	"syc-file/internal/handler/user"
	"syc-file/internal/middleware"
	"syc-file/internal/sync"
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
	ws.RegisterWSRouter(v1, db, redisClient)
	sync.RegisterSyncRouter(v1, engine)
}
