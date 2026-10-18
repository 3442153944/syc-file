package middleware

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
)

// StoragePaths 在认证后绑定响应路径转换；低级别账号只看到自己的虚拟空间。
func StoragePaths(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 这些接口的 path 是客户端菜单 URL，不是服务器文件路径。
		// 仅排除已注册的路由元数据接口，文件、空间和同步响应继续脱敏。
		switch c.FullPath() {
		case "/v1/routes", "/v1/admin/routes", "/v1/admin/routes/:id", "/v1/admin/guest-routes":
			c.Next()
			return
		}
		value, ok := c.Get("UserInfo")
		if !ok || value == nil {
			c.Next()
			return
		}
		claims, ok := value.(*token.Claims)
		if !ok {
			c.Next()
			return
		}
		uid := uint(claims.UserID)
		u, err := storage.User(db, uid)
		if err == nil && u.Level >= model.LevelSuper {
			c.Next()
			return
		}
		root := ""
		if s, p, err := storage.Load(db, uid); err == nil {
			root = storage.Root(p, s)
		}
		c.Set(response.TransformKey, func(data any) any { return storage.ClientData(root, data) })
		c.Next()
	}
}
