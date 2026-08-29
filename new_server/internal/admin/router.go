package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/internal/monitor"
)

// RegisterAdminRouter 注册管理域路由。挂在 v1 认证组下，写操作各自 requireAdmin。
//
// 路径分两组：
//   - /admin/*  管理员视角（用户、日志、配额、角色权限、粘贴快传缓存）
//   - 其余      当前用户视角（自己的设备、自己的配额、监控）
func RegisterAdminRouter(rg *gin.RouterGroup, db *gorm.DB, redisClient *redis.Client) {
	h := NewAPIHandler(db, redisClient)

	a := rg.Group("/admin")
	// 用户
	a.GET("/users", h.ListUsers)
	a.PUT("/users/:id", h.UpdateUser)
	a.POST("/users/:id/reset-password", h.ResetPassword)
	a.DELETE("/users/:id", h.DeleteUser)
	a.GET("/users/:id/roles", h.UserRoles)
	a.PUT("/users/:id/roles", h.AssignUserRoles)
	// 设备（管理员可跨用户看）
	a.GET("/devices", h.ListDevices)
	// 操作日志
	a.GET("/logs", h.ListLogs)
	a.GET("/logs/modules", h.LogModules)
	a.DELETE("/logs", h.ClearLogs)
	// 存储配额
	a.GET("/storage", h.ListStorage)
	a.PUT("/storage/:id", h.UpdateQuota)
	a.POST("/storage/:id/recalc", h.RecalcStorage)
	// 角色权限
	a.GET("/roles", h.ListRoles)
	a.POST("/roles", h.CreateRole)
	a.PUT("/roles/:id", h.UpdateRole)
	a.DELETE("/roles/:id", h.DeleteRole)
	a.GET("/permissions", h.ListPermissions)
	a.POST("/permissions", h.CreatePermission)
	a.DELETE("/permissions/:id", h.DeletePermission)
	// 缓存管理：粘贴快传的内存/落盘缓存，管理员可跨用户查看、手动清理
	a.GET("/quick-share", h.ListQuickShare)
	a.DELETE("/quick-share/:code", h.RevokeQuickShare)

	// 当前用户视角
	rg.GET("/devices", h.ListDevices)
	rg.PUT("/devices/:id", h.UpdateDevice)
	rg.POST("/devices/:id/kick", h.KickDevice)
	rg.DELETE("/devices/:id", h.DeleteDevice)
	rg.GET("/storage/mine", h.MyStorage)

	// 监控（只读，登录即可看）
	m := rg.Group("/monitor")
	m.GET("/system", monitor.System)
	m.GET("/network", monitor.Network)
}
