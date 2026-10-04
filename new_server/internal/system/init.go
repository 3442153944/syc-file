package system

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
)

// Handler 本包全部 HTTP 处理器的载体。
type Handler struct {
	db *gorm.DB
}

func jsonOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": data})
}

func jsonErr(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg, "data": nil})
}

// RegistrationOpen 当前是否开放自助注册：已初始化，且至少有一个超级管理员打开了 allow_register。
// 开关存在用户表 user.allow_register（只对 level=3 的行有意义）。
func RegistrationOpen(db *gorm.DB) bool {
	if !Initialized() {
		return false
	}
	var n int64
	if err := db.Model(&model.User{}).
		Where("level = ? AND allow_register = ?", model.LevelSuper, true).Count(&n).Error; err != nil {
		return false
	}
	return n > 0
}

// Status GET /v1/system/status（免登录）—— 客户端启动先调它：未初始化就进向导。
// 只返回判定下一步所必需的信息。
func (h *Handler) Status(c *gin.Context) {
	jsonOK(c, gin.H{
		"initialized":       Initialized(),
		"registration_open": RegistrationOpen(h.db),
		"version":           config.Version,
		"mode":              config.Conf.Server.Mode,
		"name":              config.Conf.Server.Name,
	})
}

// Init POST /v1/system/init（免登录，仅未初始化时可用）
//
// 请求：{setup_code, username, password, email?, allow_register?}
// 防抢注：必须带服务器启动时打印在日志里的一次性初始化码；连续错 5 次锁 60 秒；成功后码立即作废。
func (h *Handler) Init(c *gin.Context) {
	var req struct {
		SetupCode     string `json:"setup_code"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		Email         string `json:"email"`
		AllowRegister bool   `json:"allow_register"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数格式错误")
		return
	}

	initMu.Lock()
	defer initMu.Unlock()

	if initialized.Load() {
		jsonErr(c, 409, "服务器已初始化")
		return
	}
	if time.Now().Before(lockUntil) {
		jsonErr(c, 429, "尝试过于频繁，请稍后再试")
		return
	}
	if setupCode == "" ||
		subtle.ConstantTimeCompare([]byte(normalizeCode(req.SetupCode)), []byte(normalizeCode(setupCode))) != 1 {
		failCount++
		if failCount >= 5 {
			failCount = 0
			lockUntil = time.Now().Add(60 * time.Second)
		}
		logger.Logger.Warn("初始化码错误", zap.String("ip", c.ClientIP()))
		jsonErr(c, 403, "初始化码错误")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 {
		jsonErr(c, 400, "用户名至少 3 位")
		return
	}
	if len(req.Password) < 8 {
		jsonErr(c, 400, "超级管理员密码至少 8 位")
		return
	}
	hashed, err := password.HashPassword(req.Password)
	if err != nil {
		jsonErr(c, 500, "密码加密失败")
		return
	}

	u := model.User{
		Username:      req.Username,
		Password:      hashed,
		Role:          model.RoleOfLevel(model.LevelSuper),
		Level:         model.LevelSuper,
		AllowRegister: req.AllowRegister,
		Status:        1,
	}
	if e := strings.TrimSpace(req.Email); e != "" {
		u.Email = &e
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		return setSetting(tx, settingInitialized, "true")
	}); err != nil {
		logger.Logger.Error("初始化失败", zap.Error(err))
		jsonErr(c, 500, "初始化失败: "+err.Error())
		return
	}

	initialized.Store(true)
	setupCode = ""
	failCount = 0
	logger.Logger.Info("服务器初始化完成", zap.Uint("super_admin_id", u.ID), zap.String("username", u.Username))
	jsonOK(c, gin.H{"user_id": u.ID, "username": u.Username})
}
