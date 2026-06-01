package ws

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrConnectionClosed = errors.New("connection is closed")
	ErrSendTimeout      = errors.New("send timeout")
	ErrUserNotFound     = errors.New("user not found")
	ErrConnNotFound     = errors.New("connection not found")
	ErrDeviceNotFound   = errors.New("device not found")
)

type MessageType string

const (
	MessageTypeText      MessageType = "text"
	MessageTypeBroadcast MessageType = "broadcast"
	MessageTypeSystem    MessageType = "system"
	MessageTypeHeartbeat MessageType = "heartbeat"
	MessageTypeAck       MessageType = "ack"
	MessageTypeFileSync  MessageType = "file_sync"
	MessageTypeNotify    MessageType = "notification"
)

type TargetType string

const (
	TargetTypeUser   TargetType = "user"
	TargetTypeConn   TargetType = "conn"
	TargetTypeDevice TargetType = "device"
	TargetTypeGroup  TargetType = "group"
	TargetTypeAll    TargetType = "all"
)

type DeviceType string

const (
	DeviceTypeUnknown DeviceType = "unknown"
	DeviceTypeWeb     DeviceType = "web"
	DeviceTypeAndroid DeviceType = "android"
	DeviceTypeIOS     DeviceType = "ios"
	DeviceTypeDesktop DeviceType = "desktop"
	DeviceTypeServer  DeviceType = "server"
)

type DeviceStatus string

const (
	DeviceStatusOnline  DeviceStatus = "online"
	DeviceStatusAway    DeviceStatus = "away"
	DeviceStatusBusy    DeviceStatus = "busy"
	DeviceStatusOffline DeviceStatus = "offline"
)

type Message struct {
	ID        string          `json:"id,omitempty"`
	Type      MessageType     `json:"type"`
	From      *Sender         `json:"from,omitempty"`
	Target    *Target         `json:"target"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
	Extra     json.RawMessage `json:"extra,omitempty"`
}

type Sender struct {
	UserID   uint   `json:"user_id"`
	ConnID   string `json:"conn_id,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
}

type Target struct {
	Type      TargetType `json:"type"`
	UserIDs   []uint     `json:"user_ids,omitempty"`
	ConnIDs   []string   `json:"conn_ids,omitempty"`
	DeviceIDs []string   `json:"device_ids,omitempty"`
	Groups    []string   `json:"groups,omitempty"`
}

type DeviceInfo struct {
	DeviceID   string                 `json:"device_id"`
	DeviceType DeviceType             `json:"device_type"`
	DeviceName string                 `json:"device_name"`
	Status     DeviceStatus           `json:"status"`
	Platform   string                 `json:"platform"`
	AppVersion string                 `json:"app_version"`
	PushToken  string                 `json:"push_token"`
	Extra      map[string]interface{} `json:"extra,omitempty"`
}

type ConnectionInfo struct {
	ConnID        string       `json:"conn_id"`
	UserID        uint         `json:"user_id"`
	Device        *DeviceInfo  `json:"device"`
	IP            string       `json:"ip"`
	ConnectedAt   time.Time    `json:"connected_at"`
	LastHeartbeat time.Time    `json:"last_heartbeat"`
	Status        DeviceStatus `json:"status"`
}

type UserConnectionsInfo struct {
	UserID      uint              `json:"user_id"`
	Connections []*ConnectionInfo `json:"connections"`
	TotalCount  int               `json:"total_count"`
}

const (
	WriteWait       = 10 * time.Second
	PongWait        = 60 * time.Second
	PingPeriod      = (PongWait * 9) / 10
	MaxMessageSize  = 512 * 1024
	SendChannelSize = 256
)

func NewMessage(msgType MessageType, content interface{}) *Message {
	data, _ := json.Marshal(content)
	return &Message{
		ID:        generateUUID(),
		Type:      msgType,
		Content:   data,
		Timestamp: time.Now().Unix(),
	}
}

func NewTargetUser(userIDs ...uint) *Target {
	return &Target{Type: TargetTypeUser, UserIDs: userIDs}
}

func NewTargetConn(connIDs ...string) *Target {
	return &Target{Type: TargetTypeConn, ConnIDs: connIDs}
}

func NewTargetDevice(deviceIDs ...string) *Target {
	return &Target{Type: TargetTypeDevice, DeviceIDs: deviceIDs}
}

func NewTargetGroup(groups ...string) *Target {
	return &Target{Type: TargetTypeGroup, Groups: groups}
}

func NewTargetAll() *Target {
	return &Target{Type: TargetTypeAll}
}
