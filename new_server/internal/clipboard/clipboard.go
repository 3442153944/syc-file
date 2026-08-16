// Package clipboard 剪贴板同步：一台设备复制的内容推给同一用户的其它在线设备。
//
// ── 与文件同步的区别（决定了实现有多简单）──────────────────
// 不落盘、不进 MySQL、无 CAS、无版本、无冲突。内容只在 Redis 里留一小段时间供「历史列表」用，
// 过期即消失。所以本包不碰 sync 引擎的任何东西。
//
// ── 隐私红线（剪贴板里全是密码和验证码，必须当敏感数据对待）──
//  1. 服务端**只在 Redis 存短 TTL 历史**（默认 24h / 50 条），绝不写 MySQL、绝不写日志文件；
//  2. 路径已加入操作日志中间件的 skipLogging——否则内容原文会被抄进 operation_log 表；
//  3. 是否推送由**客户端开关**决定，服务端不主动采集，默认关闭的责任在端上；
//  4. 单条大小上限 maxContentBytes，超限直接拒绝（复制一整本书没有意义，也防打爆 Redis）；
//  5. 传输走 wss/https，但**服务端能看到明文**——这一点要在客户端设置页对用户讲明白。
package clipboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"syc-file/internal/ws"
	"syc-file/pkg/token"
)

const (
	// maxContentBytes 单条剪贴板内容上限（64KiB）。文本剪贴板极少超过这个量级。
	maxContentBytes = 64 * 1024
	// historyLimit 每个用户最多留多少条历史。
	historyLimit = 50
	// historyTTL 历史保留时长。到点整串 key 过期，不做逐条清理。
	historyTTL = 24 * time.Hour
	// ContentTypeText 目前只支持文本；图片/文件走文件上传通道，不塞这里。
	ContentTypeText = "text"
)

// Item 一条剪贴板记录。
type Item struct {
	ID          string `json:"id"`
	UserID      uint   `json:"user_id"`
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	Size        int    `json:"size"`
	CreatedAt   int64  `json:"created_at"`
}

// APIHandler 剪贴板处理器。
type APIHandler struct {
	rdb *redis.Client
}

func NewAPIHandler(rdb *redis.Client) *APIHandler {
	return &APIHandler{rdb: rdb}
}

func historyKey(userID uint) string {
	return fmt.Sprintf("clipboard:history:%d", userID)
}

// RegisterClipboardRouter 注册路由（挂在 v1 认证组下）。
func RegisterClipboardRouter(rg *gin.RouterGroup, rdb *redis.Client) {
	h := NewAPIHandler(rdb)
	g := rg.Group("/clipboard")
	g.POST("/push", h.Push)
	g.GET("/history", h.History)
	g.DELETE("/history", h.ClearHistory)
}

// Push POST /v1/clipboard/push —— 上报本机剪贴板内容，服务端存历史并推给同用户其它在线设备。
//
// body: {"content":"...", "content_type":"text", "device_id":"...", "device_name":"..."}
func (h *APIHandler) Push(c *gin.Context) {
	userID, ok := currentUser(c)
	if !ok {
		return
	}
	var req struct {
		Content     string `json:"content"`
		ContentType string `json:"content_type"`
		DeviceID    string `json:"device_id"`
		DeviceName  string `json:"device_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if req.Content == "" {
		jsonErr(c, 400, "内容为空")
		return
	}
	if len(req.Content) > maxContentBytes {
		jsonErr(c, 400, fmt.Sprintf("内容超过上限（%d KB）", maxContentBytes/1024))
		return
	}
	if req.ContentType == "" {
		req.ContentType = ContentTypeText
	}

	item := Item{
		ID:          fmt.Sprintf("%d-%d", time.Now().UnixNano(), userID),
		UserID:      userID,
		DeviceID:    req.DeviceID,
		DeviceName:  req.DeviceName,
		ContentType: req.ContentType,
		Content:     req.Content,
		Size:        len(req.Content),
		CreatedAt:   time.Now().Unix(),
	}

	h.saveHistory(item)
	// 推给同一用户的其它设备。**必须排除来源设备**：它本来就有这份内容，
	// 推回去会被它的剪贴板监听当成一次新变更再传上来 —— 和文件同步那个
	// 「下载→探测→原样回传」的乒乓是同一个坑，这里从源头掐掉。
	delivered := deliverToOtherDevices(item)

	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
		"item": item, "delivered": delivered,
	}})
}

// History GET /v1/clipboard/history?limit=20
func (h *APIHandler) History(c *gin.Context) {
	userID, ok := currentUser(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > historyLimit {
		limit = historyLimit
	}
	ctx := context.Background()
	raw, err := h.rdb.LRange(ctx, historyKey(userID), 0, int64(limit-1)).Result()
	if err != nil && err != redis.Nil {
		jsonErr(c, 500, "读取历史失败: "+err.Error())
		return
	}
	items := make([]Item, 0, len(raw))
	for _, s := range raw {
		var it Item
		if json.Unmarshal([]byte(s), &it) == nil {
			items = append(items, it)
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": items})
}

// ClearHistory DELETE /v1/clipboard/history
func (h *APIHandler) ClearHistory(c *gin.Context) {
	userID, ok := currentUser(c)
	if !ok {
		return
	}
	h.rdb.Del(context.Background(), historyKey(userID))
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": nil})
}

// saveHistory 入队 + 截断 + 续期。失败不影响推送（历史是附加价值，推送才是主线）。
func (h *APIHandler) saveHistory(item Item) {
	if h.rdb == nil {
		return
	}
	data, err := json.Marshal(item)
	if err != nil {
		return
	}
	ctx := context.Background()
	key := historyKey(item.UserID)
	pipe := h.rdb.TxPipeline()
	pipe.LPush(ctx, key, data)
	pipe.LTrim(ctx, key, 0, historyLimit-1)
	pipe.Expire(ctx, key, historyTTL)
	_, _ = pipe.Exec(ctx)
}

// deliverToOtherDevices 把内容经 WS 推给该用户除来源设备外的所有在线连接，返回送达连接数。
func deliverToOtherDevices(item Item) int {
	hub := ws.GetHub()
	conns := hub.GetUserConnections(item.UserID)
	sent := 0
	for _, conn := range conns {
		if conn.Device != nil && conn.Device.DeviceID == item.DeviceID {
			continue // 来源设备
		}
		if err := ws.SendToConn(conn.ID, ws.MessageTypeClipboard, item); err == nil {
			sent++
		}
	}
	return sent
}

func currentUser(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		jsonErr(c, 401, "未授权")
		return 0, false
	}
	return uint(claimsAny.(*token.Claims).UserID), true
}

func jsonErr(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg, "data": nil})
}
