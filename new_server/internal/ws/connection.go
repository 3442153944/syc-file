package ws

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"syc-file/pkg/logger"
)

type Connection struct {
	ID            string
	UserID        uint
	Device        *DeviceInfo
	Conn          *websocket.Conn
	SendChan      chan []byte
	Hub           *Hub
	IsAlive       bool
	ConnectedAt   time.Time
	LastHeartbeat time.Time
	IP            string
	mu            sync.RWMutex
	closeChan     chan struct{}
	closeOnce     sync.Once
	metadata      map[string]interface{}
}

func NewConnection(userID uint, conn *websocket.Conn, hub *Hub, device *DeviceInfo) *Connection {
	if device == nil {
		device = &DeviceInfo{
			DeviceID:   generateUUID(),
			DeviceType: DeviceTypeUnknown,
			Status:     DeviceStatusOnline,
		}
	}
	if device.DeviceID == "" {
		device.DeviceID = generateUUID()
	}

	return &Connection{
		ID:            generateConnectionID(userID),
		UserID:        userID,
		Device:        device,
		Conn:          conn,
		SendChan:      make(chan []byte, SendChannelSize),
		Hub:           hub,
		IsAlive:       true,
		ConnectedAt:   time.Now(),
		LastHeartbeat: time.Now(),
		closeChan:     make(chan struct{}),
		metadata:      make(map[string]interface{}),
	}
}

func (c *Connection) Start() {
	c.Hub.Register(c)
	go c.readPump()
	go c.writePump()
	go c.heartbeatCheck()

	logger.Logger.Info("WebSocket连接已建立",
		zap.String("conn_id", c.ID),
		zap.Uint("user_id", c.UserID),
		zap.String("device_id", c.Device.DeviceID),
		zap.String("device_type", string(c.Device.DeviceType)),
	)
}

func (c *Connection) readPump() {
	defer c.Close()

	c.Conn.SetReadLimit(MaxMessageSize)
	_ = c.Conn.SetReadDeadline(time.Now().Add(PongWait))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(PongWait))
		c.updateHeartbeat()
		return nil
	})

	for {
		select {
		case <-c.closeChan:
			return
		default:
			messageType, data, err := c.Conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					logger.Logger.Error("WebSocket读取错误", zap.String("conn_id", c.ID), zap.Error(err))
				}
				return
			}

			switch messageType {
			case websocket.TextMessage:
				c.handleTextMessage(data)
			case websocket.BinaryMessage:
				c.handleBinaryMessage(data)
			}
		}
	}
}

func (c *Connection) writePump() {
	ticker := time.NewTicker(PingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case message, ok := <-c.SendChan:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				logger.Logger.Error("WebSocket写入错误", zap.String("conn_id", c.ID), zap.Error(err))
				return
			}

			n := len(c.SendChan)
			for i := 0; i < n; i++ {
				if msg, ok := <-c.SendChan; ok {
					if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
						return
					}
				}
			}

		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.closeChan:
			return
		}
	}
}

func (c *Connection) handleTextMessage(data []byte) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		logger.Logger.Error("消息解析失败", zap.String("conn_id", c.ID), zap.Error(err))
		c.sendError("消息格式错误")
		return
	}

	msg.From = &Sender{
		UserID:   c.UserID,
		ConnID:   c.ID,
		DeviceID: c.Device.DeviceID,
	}
	msg.Timestamp = time.Now().Unix()

	switch msg.Type {
	case MessageTypeHeartbeat:
		c.handleHeartbeat()
	default:
		c.Hub.RouteMessage(c, &msg)
	}
}

func (c *Connection) handleBinaryMessage(data []byte) {
	logger.Logger.Debug("收到二进制消息", zap.String("conn_id", c.ID), zap.Int("size", len(data)))
}

func (c *Connection) handleHeartbeat() {
	c.updateHeartbeat()
	ack := NewMessage(MessageTypeAck, map[string]interface{}{"type": "heartbeat_ack"})
	_ = c.SendMessage(ack)
}

func (c *Connection) heartbeatCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mu.RLock()
			lastHB := c.LastHeartbeat
			c.mu.RUnlock()

			if time.Since(lastHB) > 90*time.Second {
				logger.Logger.Warn("连接心跳超时", zap.String("conn_id", c.ID), zap.Uint("user_id", c.UserID))
				c.Close()
				return
			}

		case <-c.closeChan:
			return
		}
	}
}

func (c *Connection) Send(data []byte) error {
	c.mu.RLock()
	if !c.IsAlive {
		c.mu.RUnlock()
		return ErrConnectionClosed
	}
	c.mu.RUnlock()

	select {
	case c.SendChan <- data:
		return nil
	case <-time.After(5 * time.Second):
		return ErrSendTimeout
	}
}

func (c *Connection) SendMessage(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.Send(data)
}

func (c *Connection) SendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Send(data)
}

func (c *Connection) sendError(errMsg string) {
	msg := NewMessage(MessageTypeSystem, map[string]interface{}{"error": errMsg})
	_ = c.SendMessage(msg)
}

func (c *Connection) updateHeartbeat() {
	c.mu.Lock()
	c.LastHeartbeat = time.Now()
	c.mu.Unlock()
}

func (c *Connection) SetMetadata(key string, value interface{}) {
	c.mu.Lock()
	c.metadata[key] = value
	c.mu.Unlock()
}

func (c *Connection) GetMetadata(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, exists := c.metadata[key]
	return value, exists
}

func (c *Connection) GetInfo() *ConnectionInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return &ConnectionInfo{
		ConnID:        c.ID,
		UserID:        c.UserID,
		Device:        c.Device,
		IP:            c.IP,
		ConnectedAt:   c.ConnectedAt,
		LastHeartbeat: c.LastHeartbeat,
		Status:        c.Device.Status,
	}
}

func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.IsAlive = false
		c.Device.Status = DeviceStatusOffline
		c.mu.Unlock()

		c.Hub.Unregister(c)

		close(c.closeChan)
		close(c.SendChan)

		_ = c.Conn.Close()

		logger.Logger.Info("WebSocket连接已关闭",
			zap.String("conn_id", c.ID),
			zap.Uint("user_id", c.UserID),
			zap.String("device_id", c.Device.DeviceID),
		)
	})
}

func generateConnectionID(userID uint) string {
	return fmt.Sprintf("conn-%d-%s", userID, uuid.New().String()[:8])
}

func generateUUID() string {
	return uuid.New().String()
}
