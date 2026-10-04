package system

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
)

func callerLevelOf(c *gin.Context) int8 { return middleware.LevelOf(c) }

// SetUserLevel PUT /v1/admin/users/:id/level —— 在用户(1)与管理员(2)之间调整级别，仅超级管理员。
//
// 超级管理员全系统只有一个：不能把任何人设为超管，超管自己的级别也不能改。
//
// 不能改自己（避免系统里最后一个超级管理员把自己降掉）；不能用它改访客（访客只走 /admin/guests）。
// 同步写 role，保持 role 与 level 一致（见 model.RoleOfLevel）。级别在目标用户下次登录 / token 续期时生效。
func (h *Handler) SetUserLevel(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Level int8 `json:"level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if req.Level < model.LevelUser || req.Level > model.LevelAdmin {
		jsonErr(c, 400, "level 只能是 1(用户) / 2(管理员)；超级管理员只有一个，不能再设置")
		return
	}
	if uint(id) == callerID(c) {
		jsonErr(c, 400, "不能修改自己的级别")
		return
	}
	var target model.User
	if err := h.db.First(&target, id).Error; err != nil {
		jsonErr(c, 404, "用户不存在")
		return
	}
	if target.Level >= model.LevelSuper {
		jsonErr(c, 400, "不能修改超级管理员的级别")
		return
	}
	if target.Level == model.LevelGuest {
		jsonErr(c, 400, "访客账号请在访客管理里处理")
		return
	}
	updates := map[string]interface{}{"level": req.Level, "role": model.RoleOfLevel(req.Level)}
	if req.Level != model.LevelSuper {
		updates["allow_register"] = false // 开关只对超级管理员行有意义，降级时清掉避免残留生效
	}
	if err := h.db.Model(&target).Updates(updates).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	// 降级的人立刻踢下线，逼他拿新级别重新登录
	if req.Level < target.Level {
		ws.GetHub().DisconnectUser(target.ID)
	}
	logger.Logger.Info("用户级别已调整",
		zap.Uint("user_id", target.ID), zap.Int8("from", target.Level), zap.Int8("to", req.Level))
	jsonOK(c, nil)
}

// GetSettings GET /v1/admin/settings —— 系统设置（目前只有注册开关），管理员及以上可读。
func (h *Handler) GetSettings(c *gin.Context) {
	if !requireLevel(c, model.LevelAdmin) {
		return
	}
	jsonOK(c, gin.H{"allow_register": RegistrationOpen(h.db)})
}

// SetRegister PUT /v1/admin/settings/register {allow} —— 打开 / 关闭自助注册，仅超级管理员。
// 开关落在用户表的 allow_register 字段上：对所有超级管理员行统一设置。
func (h *Handler) SetRegister(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	var req struct {
		Allow *bool `json:"allow"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Allow == nil {
		jsonErr(c, 400, "需要 allow 参数")
		return
	}
	if err := h.db.Model(&model.User{}).Where("level = ?", model.LevelSuper).
		Update("allow_register", *req.Allow).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	logger.Logger.Info("注册开关已调整", zap.Bool("allow", *req.Allow))
	jsonOK(c, gin.H{"allow_register": *req.Allow})
}

// RegisterRouter 注册本包路由，并把查库钩子交给 middleware（避免 middleware 反向依赖本包）。
func RegisterRouter(rg *gin.RouterGroup, db *gorm.DB) {
	h := &Handler{db: db}
	middleware.SetUserLookup(h.lookup)
	middleware.SetGuestGuard(h.guestGuard)
	middleware.SetGuestAlive(h.guestAlive)

	s := rg.Group("/system")
	s.GET("/status", h.Status)
	s.POST("/init", h.Init)

	rg.GET("/routes", h.MyRoutes)

	a := rg.Group("/admin")
	a.GET("/routes", h.AdminListRoutes)
	a.PUT("/routes/:id", h.AdminUpdateRoute)
	a.GET("/guest-routes", h.GuestGrantableRoutes)
	a.GET("/guests", h.ListGuests)
	a.POST("/guests", h.CreateGuest)
	a.PUT("/guests/:id", h.UpdateGuest)
	a.DELETE("/guests/:id", h.DeleteGuest)
	a.POST("/users", h.CreateUser)
	a.PUT("/users/:id/level", h.SetUserLevel)
	a.GET("/settings", h.GetSettings)
	a.PUT("/settings/register", h.SetRegister)
}
