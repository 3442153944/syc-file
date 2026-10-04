package system

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
)

// 访客账号：管理员下发的临时账号（level=0），带过期时间，只能访问被指定的页面。
// 创建 / 管理都要求管理员(2)及以上；管理员只能管自己创建的，超级管理员能管全部。

const (
	maxGuestHours     = 24 * 30
	defaultGuestHours = 24
	pwAlphabet        = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type guestDTO struct {
	ID         uint       `json:"id"`
	Username   string     `json:"username"`
	Status     int8       `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Expired    bool       `json:"expired"`
	CreatedBy  *uint      `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastLogin  *time.Time `json:"last_login"`
	RouteCodes []string   `json:"route_codes"`
}

func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = pwAlphabet[int(v)%len(pwAlphabet)]
	}
	return string(out), nil
}

// validateGuestRoutes 校验授权的页面：必须存在、启用、是真页面（非目录），且是普通用户级别就能看的
// （游客不可能被授予管理类页面，那些页面背后的接口也不会对游客放行）。
func (h *Handler) validateGuestRoutes(codes []string) ([]string, string) {
	seen := map[string]bool{}
	clean := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		clean = append(clean, c)
	}
	if len(clean) == 0 {
		return nil, "至少授权一个页面"
	}
	var rows []model.Route
	h.db.Where("code IN ?", clean).Find(&rows)
	ok := map[string]bool{}
	for _, r := range rows {
		if r.Enabled && r.Component != "" && r.MinLevel <= model.LevelUser {
			ok[r.Code] = true
		}
	}
	for _, c := range clean {
		if !ok[c] {
			return nil, "页面不可授权给访客: " + c
		}
	}
	return clean, ""
}

// CreateGuest POST /v1/admin/guests
// 请求 {expire_hours?, route_codes[]}；响应里的明文密码**只此一次**，管理员自己转交给访客。
func (h *Handler) CreateGuest(c *gin.Context) {
	if !requireLevel(c, model.LevelAdmin) {
		return
	}
	var req struct {
		ExpireHours int      `json:"expire_hours"`
		RouteCodes  []string `json:"route_codes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if req.ExpireHours <= 0 {
		req.ExpireHours = defaultGuestHours
	}
	if req.ExpireHours > maxGuestHours {
		jsonErr(c, 400, "有效期最长 720 小时（30 天）")
		return
	}
	codes, msg := h.validateGuestRoutes(req.RouteCodes)
	if msg != "" {
		jsonErr(c, 400, msg)
		return
	}

	pw, err := randomPassword(10)
	if err != nil {
		jsonErr(c, 500, "生成密码失败")
		return
	}
	hashed, err := password.HashPassword(pw)
	if err != nil {
		jsonErr(c, 500, "密码加密失败")
		return
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	expires := time.Now().Add(time.Duration(req.ExpireHours) * time.Hour)
	by := callerID(c)
	u := model.User{
		Username:  "guest_" + hex.EncodeToString(suffix),
		Password:  hashed,
		Role:      model.RoleOfLevel(model.LevelGuest),
		Status:    1,
		ExpiresAt: &expires,
		CreatedBy: &by,
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		// Create 时 level=0 会被 gorm 当零值套用默认 1，必须显式落成 0
		if err := tx.Model(&u).Update("level", model.LevelGuest).Error; err != nil {
			return err
		}
		for _, code := range codes {
			if err := tx.Create(&model.UserRoute{UserID: u.ID, RouteCode: code}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		logger.Logger.Error("创建访客失败", zap.Error(err))
		jsonErr(c, 500, "创建失败: "+err.Error())
		return
	}
	logger.Logger.Info("已创建访客账号", zap.Uint("guest_id", u.ID), zap.Uint("by", by), zap.Time("expires_at", expires))
	jsonOK(c, gin.H{
		"id": u.ID, "username": u.Username, "password": pw,
		"expires_at": expires, "route_codes": codes,
	})
}

// manageableGuest 取访客并校验调用者有权管理。失败时已写响应。
func (h *Handler) manageableGuest(c *gin.Context) (*model.User, bool) {
	if !requireLevel(c, model.LevelAdmin) {
		return nil, false
	}
	id, ok := idParam(c)
	if !ok {
		return nil, false
	}
	var g model.User
	if err := h.db.First(&g, id).Error; err != nil || g.Level != model.LevelGuest {
		jsonErr(c, 404, "访客不存在")
		return nil, false
	}
	// 管理员只能管自己下发的；超级管理员不受限
	if callerLevelOf(c) < model.LevelSuper && (g.CreatedBy == nil || *g.CreatedBy != callerID(c)) {
		jsonErr(c, 403, "只能管理自己下发的访客")
		return nil, false
	}
	return &g, true
}

// ListGuests GET /v1/admin/guests
func (h *Handler) ListGuests(c *gin.Context) {
	if !requireLevel(c, model.LevelAdmin) {
		return
	}
	q := h.db.Model(&model.User{}).Where("level = ?", model.LevelGuest)
	if callerLevelOf(c) < model.LevelSuper {
		q = q.Where("created_by = ?", callerID(c))
	}
	var users []model.User
	if err := q.Order("id desc").Limit(500).Find(&users).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	grants := map[uint][]string{}
	if len(ids) > 0 {
		var rows []model.UserRoute
		h.db.Where("user_id IN ?", ids).Find(&rows)
		for _, r := range rows {
			grants[r.UserID] = append(grants[r.UserID], r.RouteCode)
		}
	}
	now := time.Now()
	out := make([]guestDTO, 0, len(users))
	for _, u := range users {
		rc := grants[u.ID]
		if rc == nil {
			rc = []string{}
		}
		out = append(out, guestDTO{
			ID: u.ID, Username: u.Username, Status: u.Status, ExpiresAt: u.ExpiresAt,
			Expired:   u.ExpiresAt == nil || now.After(*u.ExpiresAt),
			CreatedBy: u.CreatedBy, CreatedAt: u.CreatedAt, LastLogin: u.LastLogin, RouteCodes: rc,
		})
	}
	jsonOK(c, out)
}

// UpdateGuest PUT /v1/admin/guests/:id
// 可选字段：expire_hours（从现在起重新计时）、route_codes（整体替换授权页面）、status、reset_password。
func (h *Handler) UpdateGuest(c *gin.Context) {
	g, ok := h.manageableGuest(c)
	if !ok {
		return
	}
	var req struct {
		ExpireHours   *int      `json:"expire_hours"`
		RouteCodes    *[]string `json:"route_codes"`
		Status        *int8     `json:"status"`
		ResetPassword bool      `json:"reset_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	updates := map[string]interface{}{}
	if req.ExpireHours != nil {
		if *req.ExpireHours <= 0 || *req.ExpireHours > maxGuestHours {
			jsonErr(c, 400, "有效期需在 1~720 小时之间")
			return
		}
		updates["expires_at"] = time.Now().Add(time.Duration(*req.ExpireHours) * time.Hour)
	}
	if req.Status != nil {
		if *req.Status != 0 && *req.Status != 1 {
			jsonErr(c, 400, "status 只能是 0 或 1")
			return
		}
		updates["status"] = *req.Status
	}
	var newPw string
	if req.ResetPassword {
		pw, err := randomPassword(10)
		if err != nil {
			jsonErr(c, 500, "生成密码失败")
			return
		}
		hashed, err := password.HashPassword(pw)
		if err != nil {
			jsonErr(c, 500, "密码加密失败")
			return
		}
		updates["password"] = hashed
		newPw = pw
	}
	var codes []string
	if req.RouteCodes != nil {
		var msg string
		if codes, msg = h.validateGuestRoutes(*req.RouteCodes); msg != "" {
			jsonErr(c, 400, msg)
			return
		}
	}
	if len(updates) == 0 && req.RouteCodes == nil {
		jsonErr(c, 400, "没有需要更新的字段")
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(g).Updates(updates).Error; err != nil {
				return err
			}
		}
		if req.RouteCodes != nil {
			if err := tx.Where("user_id = ?", g.ID).Delete(&model.UserRoute{}).Error; err != nil {
				return err
			}
			for _, code := range codes {
				if err := tx.Create(&model.UserRoute{UserID: g.ID, RouteCode: code}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	// 禁用 / 重置密码后让现有连接立刻失效
	if (req.Status != nil && *req.Status != 1) || req.ResetPassword {
		ws.GetHub().DisconnectUser(g.ID)
	}
	data := gin.H{}
	if newPw != "" {
		data["password"] = newPw
	}
	jsonOK(c, data)
}

// DeleteGuest DELETE /v1/admin/guests/:id
func (h *Handler) DeleteGuest(c *gin.Context) {
	g, ok := h.manageableGuest(c)
	if !ok {
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", g.ID).Delete(&model.UserRoute{}).Error; err != nil {
			return err
		}
		return tx.Delete(g).Error
	}); err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	ws.GetHub().DisconnectUser(g.ID)
	logger.Logger.Info("已删除访客账号", zap.Uint("guest_id", g.ID))
	jsonOK(c, nil)
}
