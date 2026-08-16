package admin

import (
	"strings"

	"go.uber.org/zap"

	"github.com/gin-gonic/gin"

	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
)

// userBrief 用户列表行。**绝不包含密码字段**（model.User 的 Password 是 json:"-"，
// 这里仍显式挑字段，避免以后给 User 加敏感字段时被无意带出去）。
type userBrief struct {
	ID        uint    `json:"id"`
	Username  string  `json:"username"`
	Email     *string `json:"email"`
	Phone     *string `json:"phone"`
	Avatar    *string `json:"avatar"`
	Role      string  `json:"role"`
	Status    int8    `json:"status"`
	LastLogin *string `json:"last_login"`
	CreatedAt string  `json:"created_at"`
	// 运行期信息：该用户当前是否有设备在线
	Online bool `json:"online"`
	// 设备数（device 表登记过的）
	DeviceCount int64 `json:"device_count"`
}

// ListUsers GET /v1/admin/users?keyword=&role=&status=&page=&page_size=
func (h *APIHandler) ListUsers(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	p := parsePage(c)
	q := h.db.Model(&model.User{})
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("username LIKE ? OR email LIKE ? OR phone LIKE ?", like, like, like)
	}
	if role := c.Query("role"); role != "" {
		q = q.Where("role = ?", role)
	}
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}

	var total int64
	q.Count(&total)

	var users []model.User
	if err := q.Order("id asc").Offset(p.offset()).Limit(p.PageSize).Find(&users).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	hub := ws.GetHub()
	list := make([]userBrief, 0, len(users))
	for _, u := range users {
		var devCount int64
		h.db.Model(&model.Device{}).Where("user_id = ?", u.ID).Count(&devCount)
		b := userBrief{
			ID: u.ID, Username: u.Username, Email: u.Email, Phone: u.Phone,
			Avatar: u.Avatar, Role: u.Role, Status: u.Status,
			CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
			Online:      hub.IsUserOnline(u.ID),
			DeviceCount: devCount,
		}
		if u.LastLogin != nil {
			s := u.LastLogin.Format("2006-01-02 15:04:05")
			b.LastLogin = &s
		}
		list = append(list, b)
	}
	jsonPage(c, list, total, p)
}

// UpdateUser PUT /v1/admin/users/:id —— 改角色 / 启停用 / 改联系方式。
// 用户名不给改（是登录标识，且有唯一索引，改名的连带影响不在本期范围）。
func (h *APIHandler) UpdateUser(c *gin.Context) {
	adminID, ok := requireAdmin(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Role   *string `json:"role"`
		Status *int8   `json:"status"`
		Email  *string `json:"email"`
		Phone  *string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}

	var target model.User
	if err := h.db.First(&target, id).Error; err != nil {
		jsonErr(c, 404, "用户不存在")
		return
	}

	updates := map[string]interface{}{}
	if req.Role != nil {
		if *req.Role != "admin" && *req.Role != "user" {
			jsonErr(c, 400, "role 只能是 admin 或 user")
			return
		}
		// 不允许把自己降级：管理员把自己降成 user 后就再也进不来了（系统可能没有第二个管理员）
		if uint(id) == adminID && *req.Role != "admin" {
			jsonErr(c, 400, "不能修改自己的管理员角色")
			return
		}
		updates["role"] = *req.Role
	}
	if req.Status != nil {
		if uint(id) == adminID && *req.Status != 1 {
			jsonErr(c, 400, "不能禁用自己")
			return
		}
		updates["status"] = *req.Status
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}
	if req.Phone != nil {
		updates["phone"] = *req.Phone
	}
	if len(updates) == 0 {
		jsonErr(c, 400, "没有需要更新的字段")
		return
	}
	if err := h.db.Model(&target).Updates(updates).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	// 被禁用的用户立刻踢下线，否则它手里的 token 在过期前仍能继续用 WS 同步
	if req.Status != nil && *req.Status != 1 {
		ws.GetHub().DisconnectUser(target.ID)
		logger.Logger.Info("用户被禁用，已断开其全部连接", zap.Uint("user_id", target.ID))
	}
	jsonOK(c, nil)
}

// ResetPassword POST /v1/admin/users/:id/reset-password —— 管理员重置他人密码。
// 不返回明文，由管理员把新密码交给用户；重置后强制踢下线。
func (h *APIHandler) ResetPassword(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if len(req.NewPassword) < 6 {
		jsonErr(c, 400, "新密码至少 6 位")
		return
	}
	var target model.User
	if err := h.db.First(&target, id).Error; err != nil {
		jsonErr(c, 404, "用户不存在")
		return
	}
	hashed, err := password.HashPassword(req.NewPassword)
	if err != nil {
		jsonErr(c, 500, "密码加密失败")
		return
	}
	if err := h.db.Model(&target).Update("password", hashed).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	ws.GetHub().DisconnectUser(target.ID)
	logger.Logger.Info("管理员重置了用户密码", zap.Uint("user_id", target.ID))
	jsonOK(c, nil)
}

// DeleteUser DELETE /v1/admin/users/:id
//
// 只删 user 行与其角色绑定；**不动它的文件和同步记录**——那些数据可能仍被其它设备引用，
// 静默连带删除是不可逆的破坏。需要清数据时走文件管理，不在这里顺手做。
func (h *APIHandler) DeleteUser(c *gin.Context) {
	adminID, ok := requireAdmin(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if uint(id) == adminID {
		jsonErr(c, 400, "不能删除自己")
		return
	}
	var target model.User
	if err := h.db.First(&target, id).Error; err != nil {
		jsonErr(c, 404, "用户不存在")
		return
	}
	if err := h.db.Delete(&target).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	h.db.Where("user_id = ?", id).Delete(&model.UserRole{})
	ws.GetHub().DisconnectUser(uint(id))
	logger.Logger.Warn("管理员删除了用户", zap.Uint64("user_id", id), zap.String("username", target.Username))
	jsonOK(c, nil)
}
