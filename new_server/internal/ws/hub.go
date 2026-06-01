package ws

import (
	"encoding/json"
	"sync"

	"go.uber.org/zap"

	"syc-file/pkg/logger"
)

var (
	globalHub *Hub
	hubOnce   sync.Once
)

func GetHub() *Hub {
	hubOnce.Do(func() {
		globalHub = NewHub()
		go globalHub.Run()
		logger.Logger.Info("WebSocket Hub已初始化")
	})
	return globalHub
}

type MessageHandler func(*Connection, *Message)

type Hub struct {
	connByID        map[string]*Connection
	connsByUser     map[uint]map[string]bool
	connByDevice    map[string]string
	groups          map[string]map[uint]bool
	register        chan *Connection
	unregister      chan *Connection
	broadcast       chan *Message
	messageHandlers map[MessageType]MessageHandler
	onConnect       func(*Connection)
	onDisconnect    func(*Connection)
	mu              sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		connByID:        make(map[string]*Connection),
		connsByUser:     make(map[uint]map[string]bool),
		connByDevice:    make(map[string]string),
		groups:          make(map[string]map[uint]bool),
		register:        make(chan *Connection, 256),
		unregister:      make(chan *Connection, 256),
		broadcast:       make(chan *Message, 512),
		messageHandlers: make(map[MessageType]MessageHandler),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case conn := <-h.register:
			h.handleRegister(conn)
		case conn := <-h.unregister:
			h.handleUnregister(conn)
		case msg := <-h.broadcast:
			h.handleBroadcast(msg)
		}
	}
}

func (h *Hub) handleRegister(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if oldConnID, exists := h.connByDevice[conn.Device.DeviceID]; exists {
		if oldConn, ok := h.connByID[oldConnID]; ok {
			logger.Logger.Info("同一设备已存在连接，关闭旧连接",
				zap.String("device_id", conn.Device.DeviceID),
				zap.String("old_conn_id", oldConnID),
				zap.String("new_conn_id", conn.ID),
			)
			go oldConn.Close()
		}
	}

	h.connByID[conn.ID] = conn
	h.connByDevice[conn.Device.DeviceID] = conn.ID

	if h.connsByUser[conn.UserID] == nil {
		h.connsByUser[conn.UserID] = make(map[string]bool)
	}
	h.connsByUser[conn.UserID][conn.ID] = true

	if h.onConnect != nil {
		go h.onConnect(conn)
	}

	logger.Logger.Info("连接已注册",
		zap.String("conn_id", conn.ID),
		zap.Uint("user_id", conn.UserID),
		zap.String("device_id", conn.Device.DeviceID),
		zap.Int("user_conn_count", len(h.connsByUser[conn.UserID])),
	)

	welcome := NewMessage(MessageTypeSystem, map[string]interface{}{
		"event":     "connected",
		"conn_id":   conn.ID,
		"device_id": conn.Device.DeviceID,
	})
	_ = conn.SendMessage(welcome)
}

func (h *Hub) handleUnregister(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.connByID[conn.ID]; !exists {
		return
	}

	delete(h.connByID, conn.ID)
	delete(h.connByDevice, conn.Device.DeviceID)

	if userConns, exists := h.connsByUser[conn.UserID]; exists {
		delete(userConns, conn.ID)
		if len(userConns) == 0 {
			delete(h.connsByUser, conn.UserID)
			for groupName, group := range h.groups {
				delete(group, conn.UserID)
				if len(group) == 0 {
					delete(h.groups, groupName)
				}
			}
		}
	}

	if h.onDisconnect != nil {
		go h.onDisconnect(conn)
	}

	logger.Logger.Info("连接已注销",
		zap.String("conn_id", conn.ID),
		zap.Uint("user_id", conn.UserID),
		zap.Int("remaining_conns", len(h.connByID)),
	)
}

func (h *Hub) handleBroadcast(msg *Message) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.connByID))
	for _, conn := range h.connByID {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	h.sendToConnections(conns, msg)
}

func (h *Hub) Register(conn *Connection) {
	h.register <- conn
}

func (h *Hub) Unregister(conn *Connection) {
	h.unregister <- conn
}

func (h *Hub) RouteMessage(from *Connection, msg *Message) {
	if handler, ok := h.messageHandlers[msg.Type]; ok {
		handler(from, msg)
		return
	}

	if msg.Target == nil {
		return
	}

	switch msg.Target.Type {
	case TargetTypeUser:
		h.SendToUsers(msg.Target.UserIDs, msg)
	case TargetTypeConn:
		h.SendToConns(msg.Target.ConnIDs, msg)
	case TargetTypeDevice:
		h.SendToDevices(msg.Target.DeviceIDs, msg)
	case TargetTypeGroup:
		for _, group := range msg.Target.Groups {
			h.SendToGroup(group, msg)
		}
	case TargetTypeAll:
		h.Broadcast(msg)
	}
}

func (h *Hub) Broadcast(msg *Message) {
	h.broadcast <- msg
}

func (h *Hub) SendToUsers(userIDs []uint, msg *Message) {
	h.mu.RLock()
	var conns []*Connection
	for _, userID := range userIDs {
		if connIDs, exists := h.connsByUser[userID]; exists {
			for connID := range connIDs {
				if conn, ok := h.connByID[connID]; ok {
					conns = append(conns, conn)
				}
			}
		}
	}
	h.mu.RUnlock()
	h.sendToConnections(conns, msg)
}

func (h *Hub) SendToUser(userID uint, msg *Message) error {
	h.mu.RLock()
	connIDs, exists := h.connsByUser[userID]
	if !exists || len(connIDs) == 0 {
		h.mu.RUnlock()
		return ErrUserNotFound
	}

	var conns []*Connection
	for connID := range connIDs {
		if conn, ok := h.connByID[connID]; ok {
			conns = append(conns, conn)
		}
	}
	h.mu.RUnlock()

	h.sendToConnections(conns, msg)
	return nil
}

func (h *Hub) SendToConns(connIDs []string, msg *Message) {
	h.mu.RLock()
	var conns []*Connection
	for _, connID := range connIDs {
		if conn, exists := h.connByID[connID]; exists {
			conns = append(conns, conn)
		}
	}
	h.mu.RUnlock()
	h.sendToConnections(conns, msg)
}

func (h *Hub) SendToConn(connID string, msg *Message) error {
	h.mu.RLock()
	conn, exists := h.connByID[connID]
	h.mu.RUnlock()

	if !exists {
		return ErrConnNotFound
	}
	return conn.SendMessage(msg)
}

func (h *Hub) SendToDevices(deviceIDs []string, msg *Message) {
	h.mu.RLock()
	var conns []*Connection
	for _, deviceID := range deviceIDs {
		if connID, exists := h.connByDevice[deviceID]; exists {
			if conn, ok := h.connByID[connID]; ok {
				conns = append(conns, conn)
			}
		}
	}
	h.mu.RUnlock()
	h.sendToConnections(conns, msg)
}

func (h *Hub) SendToDevice(deviceID string, msg *Message) error {
	h.mu.RLock()
	connID, exists := h.connByDevice[deviceID]
	if !exists {
		h.mu.RUnlock()
		return ErrDeviceNotFound
	}
	conn, exists := h.connByID[connID]
	h.mu.RUnlock()

	if !exists {
		return ErrConnNotFound
	}
	return conn.SendMessage(msg)
}

func (h *Hub) SendToGroup(groupName string, msg *Message) {
	h.mu.RLock()
	group, exists := h.groups[groupName]
	if !exists {
		h.mu.RUnlock()
		return
	}
	userIDs := make([]uint, 0, len(group))
	for userID := range group {
		userIDs = append(userIDs, userID)
	}
	h.mu.RUnlock()

	h.SendToUsers(userIDs, msg)
}

func (h *Hub) sendToConnections(conns []*Connection, msg *Message) {
	if len(conns) == 0 {
		return
	}
	data, err := msgToBytes(msg)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Add(1)
		go func(c *Connection) {
			defer wg.Done()
			if err := c.Send(data); err != nil {
				logger.Logger.Error("发送消息失败", zap.String("conn_id", c.ID), zap.Error(err))
			}
		}(conn)
	}
	wg.Wait()
}

func msgToBytes(msg *Message) ([]byte, error) {
	return json.Marshal(msg)
}

func (h *Hub) AddToGroup(groupName string, userID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.groups[groupName] == nil {
		h.groups[groupName] = make(map[uint]bool)
	}
	h.groups[groupName][userID] = true
}

func (h *Hub) RemoveFromGroup(groupName string, userID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if group, exists := h.groups[groupName]; exists {
		delete(group, userID)
		if len(group) == 0 {
			delete(h.groups, groupName)
		}
	}
}

func (h *Hub) GetGroupUsers(groupName string) []uint {
	h.mu.RLock()
	defer h.mu.RUnlock()

	group, exists := h.groups[groupName]
	if !exists {
		return nil
	}
	users := make([]uint, 0, len(group))
	for userID := range group {
		users = append(users, userID)
	}
	return users
}

func (h *Hub) GetConnection(connID string) (*Connection, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conn, exists := h.connByID[connID]
	return conn, exists
}

func (h *Hub) GetConnectionByDevice(deviceID string) (*Connection, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	connID, exists := h.connByDevice[deviceID]
	if !exists {
		return nil, false
	}
	conn, exists := h.connByID[connID]
	return conn, exists
}

func (h *Hub) GetUserConnections(userID uint) []*Connection {
	h.mu.RLock()
	defer h.mu.RUnlock()

	connIDs, exists := h.connsByUser[userID]
	if !exists {
		return nil
	}
	conns := make([]*Connection, 0, len(connIDs))
	for connID := range connIDs {
		if conn, ok := h.connByID[connID]; ok {
			conns = append(conns, conn)
		}
	}
	return conns
}

func (h *Hub) GetUserConnectionsInfo(userID uint) *UserConnectionsInfo {
	conns := h.GetUserConnections(userID)
	if conns == nil {
		return nil
	}
	infos := make([]*ConnectionInfo, len(conns))
	for i, conn := range conns {
		infos[i] = conn.GetInfo()
	}
	return &UserConnectionsInfo{
		UserID:      userID,
		Connections: infos,
		TotalCount:  len(infos),
	}
}

func (h *Hub) IsUserOnline(userID uint) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, exists := h.connsByUser[userID]
	return exists
}

func (h *Hub) IsDeviceOnline(deviceID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, exists := h.connByDevice[deviceID]
	return exists
}

func (h *Hub) GetOnlineUsers() []uint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	users := make([]uint, 0, len(h.connsByUser))
	for userID := range h.connsByUser {
		users = append(users, userID)
	}
	return users
}

func (h *Hub) RegisterHandler(msgType MessageType, handler MessageHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messageHandlers[msgType] = handler
}

func (h *Hub) SetConnectionHandler(onConnect, onDisconnect func(*Connection)) {
	h.onConnect = onConnect
	h.onDisconnect = onDisconnect
}

func (h *Hub) Shutdown() {
	logger.Logger.Info("正在关闭WebSocket Hub...")
	h.mu.Lock()
	conns := make([]*Connection, 0, len(h.connByID))
	for _, conn := range h.connByID {
		conns = append(conns, conn)
	}
	h.mu.Unlock()

	for _, conn := range conns {
		conn.Close()
	}
	logger.Logger.Info("WebSocket Hub已关闭")
}
