package system

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
)

// CreateUser POST /v1/admin/users —— 管理员创建账号（自助注册关闭时，这是新增用户的唯一途径）。
//
// 请求 {username, password?, email?, level?}：
//   - level 缺省 / 1 = 用户，管理员(2)及以上可创建；
//   - level 2 = 管理员，**只有超级管理员**能创建；
//   - 超级管理员全系统只有一个（初始化时产生），不能再创建 / 提升出第二个。
//
// password 留空则自动生成并**仅在本次响应里返回一次**（由创建者转交给对方）；自己指定的不会回显。
func (h *Handler) CreateUser(c *gin.Context) {
	if !requireLevel(c, model.LevelAdmin) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Level    int8   `json:"level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if len(req.Username) < 3 {
		jsonErr(c, 400, "用户名至少 3 位")
		return
	}
	if req.Level == 0 {
		req.Level = model.LevelUser
	}
	switch req.Level {
	case model.LevelUser:
	case model.LevelAdmin:
		if callerLevelOf(c) < model.LevelSuper {
			jsonErr(c, 403, "只有超级管理员能创建管理员")
			return
		}
	default:
		jsonErr(c, 400, "level 只能是 1(用户) 或 2(管理员)；超级管理员只有一个，由服务器初始化产生")
		return
	}

	generated := false
	pw := req.Password
	if pw == "" {
		var err error
		if pw, err = randomPassword(12); err != nil {
			jsonErr(c, 500, "生成密码失败")
			return
		}
		generated = true
	} else if len(pw) < 8 {
		jsonErr(c, 400, "密码至少 8 位")
		return
	}

	var n int64
	h.db.Model(&model.User{}).Where("username = ?", req.Username).Count(&n)
	if n > 0 {
		jsonErr(c, 400, "用户名已存在")
		return
	}
	if req.Email != "" {
		h.db.Model(&model.User{}).Where("email = ?", req.Email).Count(&n)
		if n > 0 {
			jsonErr(c, 400, "邮箱已被使用")
			return
		}
	}
	hashed, err := password.HashPassword(pw)
	if err != nil {
		jsonErr(c, 500, "密码加密失败")
		return
	}
	by := callerID(c)
	u := model.User{
		Username:  req.Username,
		Password:  hashed,
		Role:      model.RoleOfLevel(req.Level),
		Level:     req.Level,
		Status:    1,
		CreatedBy: &by,
	}
	if req.Email != "" {
		u.Email = &req.Email
	}
	if err := h.db.Create(&u).Error; err != nil {
		logger.Logger.Error("创建用户失败", zap.Error(err))
		jsonErr(c, 500, "创建失败: "+err.Error())
		return
	}
	logger.Logger.Info("管理员创建了账号",
		zap.Uint("user_id", u.ID), zap.String("username", u.Username), zap.Int8("level", u.Level), zap.Uint("by", by))
	data := gin.H{"id": u.ID, "username": u.Username, "level": u.Level}
	if generated {
		data["password"] = pw
	}
	jsonOK(c, data)
}
