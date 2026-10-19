package vpn

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterVPNRouter 注册组网接口。全部挂在 /v1 认证组下：
// 设备侧接口要求登录，管理接口在 handler 内按 level 与组内角色二次判定。
func RegisterVPNRouter(rg *gin.RouterGroup, db *gorm.DB) {
	h := NewAPI(db)
	g := rg.Group("/vpn")

	// 设备侧
	g.POST("/enroll", h.Enroll)
	g.POST("/register", h.Register)
	g.POST("/auto-enroll", h.AutoEnroll)
	g.GET("/peers", h.Peers)
	g.POST("/heartbeat", h.Heartbeat)
	g.GET("/devices/my", h.MyDevices)
	g.DELETE("/devices/:id", h.ReleaseDevice)
	g.GET("/link", h.Link)
	g.GET("/identity", h.Identity)

	// 管理侧
	a := g.Group("/admin")
	a.POST("/orgs", h.CreateOrg)
	a.GET("/orgs", h.ListOrgs)
	a.POST("/groups", h.CreateGroup)
	a.GET("/groups", h.ListGroups)
	a.POST("/members", h.AddMember)
	a.DELETE("/members", h.RemoveMember)
	a.GET("/devices", h.ListDevices)
	a.POST("/devices/revoke", h.RevokeDevice)
	a.PUT("/interops", h.SetInterop)
	a.GET("/interops", h.ListInterops)
	a.GET("/server-peers", h.ServerPeers)
	a.GET("/overview", h.Overview)
	a.POST("/devices/:id/kick", h.KickDevice)
	a.POST("/devices/:id/suspend", h.SuspendDevice)
	a.POST("/devices/:id/ban", h.BanDevice)
	a.DELETE("/devices/:id", h.ReleaseAdminDevice)
}
