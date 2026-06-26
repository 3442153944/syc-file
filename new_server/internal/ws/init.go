package ws

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/pkg/logger"
)

var Handler *WSHandler

type WSHandler struct {
	db *gorm.DB
}

func InitWS(db *gorm.DB) *WSHandler {
	hub := GetHub()

	hub.SetConnectionHandler(
		func(conn *Connection) {
			msg := NewMessage(MessageTypeSystem, map[string]interface{}{
				"event":       "user_online",
				"user_id":     conn.UserID,
				"conn_id":     conn.ID,
				"device_id":   conn.Device.DeviceID,
				"device_type": conn.Device.DeviceType,
			})
			hub.Broadcast(msg)
		},
		func(conn *Connection) {
			remaining := len(hub.GetUserConnections(conn.UserID))
			msg := NewMessage(MessageTypeSystem, map[string]interface{}{
				"event":                 "user_offline",
				"user_id":               conn.UserID,
				"conn_id":               conn.ID,
				"device_id":             conn.Device.DeviceID,
				"remaining_connections": remaining,
			})
			hub.Broadcast(msg)
		},
	)

	registerDefaultHandlers(hub)

	Handler = &WSHandler{db: db}
	logger.Logger.Info("WebSocket模块初始化完成")
	return Handler
}

func registerDefaultHandlers(hub *Hub) {
	hub.RegisterHandler(MessageTypeFileSync, func(conn *Connection, msg *Message) {
		logger.Logger.Debug("收到文件同步消息", zap.String("conn_id", conn.ID), zap.Uint("user_id", conn.UserID))
		var content map[string]interface{}
		if err := json.Unmarshal(msg.Content, &content); err == nil {
			if event, ok := content["event"].(string); ok && event != "" && fileSyncHandler != nil {
				fileSyncHandler(conn, msg, event, content)
				return
			}
		}
		if msg.Target != nil {
			hub.RouteMessage(conn, msg)
		}
	})

	hub.RegisterHandler(MessageTypeNotify, func(conn *Connection, msg *Message) {
		if msg.Target != nil {
			hub.RouteMessage(conn, msg)
		}
	})

	hub.RegisterHandler(MessageTypeText, func(conn *Connection, msg *Message) {
		if msg.Target != nil {
			hub.RouteMessage(conn, msg)
		}
	})

	hub.RegisterHandler(MessageTypeBroadcast, func(conn *Connection, msg *Message) {
		hub.Broadcast(msg)
	})
}

// ========== 便捷函数 ==========

func Broadcast(msgType MessageType, content interface{}) {
	msg := NewMessage(msgType, content)
	msg.Target = NewTargetAll()
	GetHub().Broadcast(msg)
}

func SendToUser(userID uint, msgType MessageType, content interface{}) error {
	msg := NewMessage(msgType, content)
	msg.Target = NewTargetUser(userID)
	return GetHub().SendToUser(userID, msg)
}

func SendToUsers(userIDs []uint, msgType MessageType, content interface{}) {
	msg := NewMessage(msgType, content)
	msg.Target = NewTargetUser(userIDs...)
	GetHub().SendToUsers(userIDs, msg)
}

func SendToConn(connID string, msgType MessageType, content interface{}) error {
	msg := NewMessage(msgType, content)
	msg.Target = NewTargetConn(connID)
	return GetHub().SendToConn(connID, msg)
}

func SendToDevice(deviceID string, msgType MessageType, content interface{}) error {
	msg := NewMessage(msgType, content)
	msg.Target = NewTargetDevice(deviceID)
	return GetHub().SendToDevice(deviceID, msg)
}

func SendToGroup(groupName string, msgType MessageType, content interface{}) {
	msg := NewMessage(msgType, content)
	msg.Target = NewTargetGroup(groupName)
	GetHub().SendToGroup(groupName, msg)
}

func NotifyUser(userID uint, title, message, level string) error {
	content := map[string]interface{}{
		"title":   title,
		"message": message,
		"level":   level,
		"time":    time.Now().Unix(),
	}
	return SendToUser(userID, MessageTypeNotify, content)
}

func NotifyDevice(deviceID string, title, message, level string) error {
	content := map[string]interface{}{
		"title":   title,
		"message": message,
		"level":   level,
		"time":    time.Now().Unix(),
	}
	return SendToDevice(deviceID, MessageTypeNotify, content)
}

func NotifyAll(title, message, level string) {
	content := map[string]interface{}{
		"title":   title,
		"message": message,
		"level":   level,
		"time":    time.Now().Unix(),
	}
	Broadcast(MessageTypeNotify, content)
}

func IsUserOnline(userID uint) bool {
	return GetHub().IsUserOnline(userID)
}

func IsDeviceOnline(deviceID string) bool {
	return GetHub().IsDeviceOnline(deviceID)
}

func GetOnlineUserCount() int {
	return len(GetHub().GetOnlineUsers())
}

func GetOnlineUserIDs() []uint {
	return GetHub().GetOnlineUsers()
}

func jsonRawHelper(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
