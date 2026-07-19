package update

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterUpdateRouter 注册应用更新相关路由。
// check/latest 任意登录用户可用；publish/list/update/delete 仅 admin。
func RegisterUpdateRouter(rg *gin.RouterGroup, db *gorm.DB) {
	h := NewAPIHandler(db)
	u := rg.Group("/update")
	u.GET("/check", h.Check)
	u.GET("/latest", h.Latest)
	u.GET("/releases", h.ListReleases)
	u.POST("/publish", h.Publish)
	u.PUT("/releases/:id", h.UpdateRelease)
	u.DELETE("/releases/:id", h.DeleteRelease)
}
