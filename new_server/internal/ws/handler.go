package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/device_store"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

type HTTPHandler struct {
	db       *gorm.DB
	upgrader websocket.Upgrader
	hub      *Hub
}

func NewHTTPHandler(db *gorm.DB) *HTTPHandler {
	return &HTTPHandler{
		db: db,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		hub: GetHub(),
	}
}

func (h *HTTPHandler) Connect(c *gin.Context) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未授权", "data": nil})
		return
	}
	userClaims := claimsAny.(*token.Claims)
	userID := uint(userClaims.UserID)

	var user model.User
	if err := h.db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 404, "message": "用户不存在", "data": nil})
		return
	}

	// Query 参数：基础设备信息
	var req struct {
		DeviceID   string `form:"device_id"`
		DeviceType string `form:"device_type"`
		DeviceName string `form:"device_name"`
		Platform   string `form:"platform"`
		AppVersion string `form:"app_version"`
	}
	_ = c.ShouldBindQuery(&req)

	// Header 里取详细设备信息（JSON 格式，客户端序列化后放进去）
	// 或者直接从 Query 里单独取每个字段也行
	var driverDetailInfo device_store.DriverDetailInfo
	if raw := c.GetHeader("X-Device-Info"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &driverDetailInfo)
	}

	// 升级 WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Logger.Error("WebSocket升级失败", zap.Uint("user_id", userID), zap.Error(err))
		return
	}

	deviceInfo := &DeviceInfo{
		DeviceID:   req.DeviceID,
		DeviceType: parseDeviceType(req.DeviceType),
		DeviceName: req.DeviceName,
		Platform:   req.Platform,
		AppVersion: req.AppVersion,
		Status:     DeviceStatusOnline,
	}

	connection := NewConnection(userID, conn, h.hub, deviceInfo)
	connection.IP = c.ClientIP()
	connection.SetMetadata("username", user.Username)
	connection.SetMetadata("role", user.Role)

	// 服务端填充，不从客户端取
	driverDetailInfo.ConnID = connection.ID
	driverDetailInfo.UserID = userID
	driverDetailInfo.DeviceID = req.DeviceID // 以 Query 参数为准
	driverDetailInfo.DeviceName = req.DeviceName
	driverDetailInfo.AppVersion = req.AppVersion

	// 存 Redis
	if err := device_store.Global.Online(context.Background(), driverDetailInfo); err != nil {
		logger.Logger.Error("设备上线记录失败", zap.Error(err))
	}

	connection.Start() // 放最后，开始阻塞读写
}

func (h *HTTPHandler) GetOnlineUsers(c *gin.Context) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未授权", "data": nil})
		return
	}
	userClaims := claimsAny.(*token.Claims)
	if !isAdmin(userClaims.Roles) {
		c.JSON(http.StatusOK, gin.H{"code": 403, "message": "权限不足，仅管理员可查看所有在线设备", "data": nil})
		return
	}
	users := h.hub.GetOnlineUsers()
	var userList []gin.H
	for _, uid := range users {
		var u model.User
		if err := h.db.First(&u, uid).Error; err == nil {
			info := h.hub.GetUserConnectionsInfo(uid)
			userList = append(userList, gin.H{
				"id":          u.ID,
				"username":    u.Username,
				"avatar":      u.Avatar,
				"role":        u.Role,
				"connections": info.Connections,
				"conn_count":  info.TotalCount,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{"total": len(userList), "users": userList}})
}

func (h *HTTPHandler) SendMessage(c *gin.Context) {
	var req struct {
		TargetType string          `json:"target_type" binding:"required"`
		UserIDs    []uint          `json:"user_ids"`
		ConnIDs    []string        `json:"conn_ids"`
		DeviceIDs  []string        `json:"device_ids"`
		Groups     []string        `json:"groups"`
		Type       string          `json:"type" binding:"required"`
		Content    json.RawMessage `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "请求参数错误", "data": nil})
		return
	}

	msg := &Message{
		ID:        generateUUID(),
		Type:      MessageType(req.Type),
		Content:   req.Content,
		Timestamp: 0, // will be set by connection
	}

	switch req.TargetType {
	case "user":
		msg.Target = NewTargetUser(req.UserIDs...)
		h.hub.SendToUsers(req.UserIDs, msg)
	case "conn":
		msg.Target = NewTargetConn(req.ConnIDs...)
		h.hub.SendToConns(req.ConnIDs, msg)
	case "device":
		msg.Target = NewTargetDevice(req.DeviceIDs...)
		h.hub.SendToDevices(req.DeviceIDs, msg)
	case "group":
		msg.Target = NewTargetGroup(req.Groups...)
		for _, group := range req.Groups {
			h.hub.SendToGroup(group, msg)
		}
	default:
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "无效的目标类型", "data": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "消息已发送", "data": nil})
}

func (h *HTTPHandler) BroadcastMessage(c *gin.Context) {
	var req struct {
		Type    string          `json:"type" binding:"required"`
		Content json.RawMessage `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "请求参数错误", "data": nil})
		return
	}

	msg := &Message{
		ID:      generateUUID(),
		Type:    MessageType(req.Type),
		Target:  NewTargetAll(),
		Content: req.Content,
	}
	h.hub.Broadcast(msg)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "消息已广播", "data": gin.H{"online": len(h.hub.GetOnlineUsers())}})
}

func (h *HTTPHandler) GetUserConnections(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "无效的用户ID", "data": nil})
		return
	}
	info := h.hub.GetUserConnectionsInfo(uint(userID))
	if info == nil {
		c.JSON(http.StatusOK, gin.H{"code": 404, "message": "用户不在线", "data": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": info})
}

func (h *HTTPHandler) DisconnectConn(c *gin.Context) {
	connID := c.Param("conn_id")
	conn, exists := h.hub.GetConnection(connID)
	if !exists {
		c.JSON(http.StatusOK, gin.H{"code": 404, "message": "连接不存在", "data": nil})
		return
	}
	conn.Close()
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "连接已断开", "data": nil})
}

func (h *HTTPHandler) DisconnectUser(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "无效的用户ID", "data": nil})
		return
	}
	conns := h.hub.GetUserConnections(uint(userID))
	if len(conns) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 404, "message": "用户不在线", "data": nil})
		return
	}
	for _, conn := range conns {
		conn.Close()
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "用户所有连接已断开", "data": nil})
}

func (h *HTTPHandler) DisconnectDevice(c *gin.Context) {
	deviceID := c.Param("device_id")
	conn, exists := h.hub.GetConnectionByDevice(deviceID)
	if !exists {
		c.JSON(http.StatusOK, gin.H{"code": 404, "message": "设备不在线", "data": nil})
		return
	}
	conn.Close()
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "设备已断开", "data": nil})
}

func (h *HTTPHandler) GetStats(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{"stats": gin.H{
		"online_users":       len(h.hub.GetOnlineUsers()),
		"active_connections": len(h.hub.GetUserConnections(0)),
	}}})
}

func (h *HTTPHandler) CreateGroup(c *gin.Context) {
	var req struct {
		GroupName string `json:"group_name" binding:"required"`
		UserIDs   []uint `json:"user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "请求参数错误", "data": nil})
		return
	}
	for _, uid := range req.UserIDs {
		h.hub.AddToGroup(req.GroupName, uid)
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "分组创建成功", "data": nil})
}

func (h *HTTPHandler) SendToGroup(c *gin.Context) {
	var req struct {
		GroupName string          `json:"group_name" binding:"required"`
		Type      string          `json:"type" binding:"required"`
		Content   json.RawMessage `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "请求参数错误", "data": nil})
		return
	}
	msg := &Message{
		ID:      generateUUID(),
		Type:    MessageType(req.Type),
		Target:  NewTargetGroup(req.GroupName),
		Content: req.Content,
	}
	h.hub.SendToGroup(req.GroupName, msg)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "消息已发送到分组", "data": nil})
}

func (h *HTTPHandler) GetGroupUsers(c *gin.Context) {
	groupName := c.Param("name")
	users := h.hub.GetGroupUsers(groupName)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{"group_name": groupName, "users": users, "count": len(users)}})
}

// GetMyDevices 返回当前用户自己的在线设备连接（所有登录用户可用）。
// 对应前端"我的在线设备"模块。
func (h *HTTPHandler) GetMyDevices(c *gin.Context) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未授权", "data": nil})
		return
	}
	userClaims := claimsAny.(*token.Claims)
	userID := uint(userClaims.UserID)
	info := h.hub.GetUserConnectionsInfo(userID)
	if info == nil {
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": &UserConnectionsInfo{
			UserID:      userID,
			Connections: []*ConnectionInfo{},
			TotalCount:  0,
		}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": info})
}

// isAdmin 判断 roles 中是否含 "admin"
func isAdmin(roles []string) bool {
	for _, r := range roles {
		if r == "admin" {
			return true
		}
	}
	return false
}

func parseDeviceType(s string) DeviceType {
	switch s {
	case "web":
		return DeviceTypeWeb
	case "android":
		return DeviceTypeAndroid
	case "ios":
		return DeviceTypeIOS
	case "desktop":
		return DeviceTypeDesktop
	case "server":
		return DeviceTypeServer
	default:
		return DeviceTypeUnknown
	}
}
