package ws

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterWSRouter(rg *gin.RouterGroup, db *gorm.DB) {
	h := NewHTTPHandler(db)
	InitWS(db)

	ws := rg.Group("/ws")
	ws.GET("/connect", h.Connect)
	ws.GET("/online", h.GetOnlineUsers)
	ws.GET("/user/:id/connections", h.GetUserConnections)
	ws.GET("/stats", h.GetStats)
	ws.POST("/send", h.SendMessage)
	ws.POST("/broadcast", h.BroadcastMessage)
	ws.DELETE("/conn/:conn_id", h.DisconnectConn)
	ws.DELETE("/user/:id", h.DisconnectUser)
	ws.DELETE("/device/:device_id", h.DisconnectDevice)
	ws.POST("/group", h.CreateGroup)
	ws.POST("/group/send", h.SendToGroup)
	ws.GET("/group/:name/users", h.GetGroupUsers)
}
