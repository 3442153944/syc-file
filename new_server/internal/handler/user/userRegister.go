package user

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/internal/system"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
)

func HandlerFuncRegister(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password" binding:"required"`
			Email    string `json:"email"`
			Phone    string `json:"phone"`
			Avatar   string `json:"avatar"`
		}

		// 1. 绑定参数
		if err := c.ShouldBindJSON(&req); err != nil {
			logger.Logger.Warn("注册请求参数错误", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"message": "参数格式错误"})
			return
		}

		// 1.5 注册开关：未初始化或关闭自助注册时拒绝（开关在用户表 user.allow_register，见 system 包）
		if !system.Initialized() {
			c.JSON(http.StatusForbidden, gin.H{"message": "服务器尚未初始化，请先在管理端完成初始化"})
			return
		}
		if !system.RegistrationOpen(db) {
			c.JSON(http.StatusForbidden, gin.H{"message": "注册已关闭，请联系管理员创建账号"})
			return
		}

		// 2. 校验密码长度
		if len(req.Password) < 6 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "密码长度至少6位"})
			return
		}

		// 3. 校验至少一个凭证
		if req.Username == "" && req.Email == "" && req.Phone == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "请提供用户名、邮箱或手机号"})
			return
		}

		// 4. 校验用户名长度
		if req.Username != "" && len(req.Username) < 3 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "用户名长度至少3位"})
			return
		}

		// 5. 检查是否已存在
		var existingUser model.User
		err := db.Where("username = ? OR email = ? OR phone = ?",
			req.Username, req.Email, req.Phone).First(&existingUser).Error

		if err == nil {
			if existingUser.Username == req.Username {
				c.JSON(http.StatusBadRequest, gin.H{"message": "用户名已存在"})
				return
			}
			if req.Email != "" && existingUser.Email != nil && *existingUser.Email == req.Email {
				c.JSON(http.StatusBadRequest, gin.H{"message": "邮箱已被注册"})
				return
			}
			if req.Phone != "" && existingUser.Phone != nil && *existingUser.Phone == req.Phone {
				c.JSON(http.StatusBadRequest, gin.H{"message": "手机号已被注册"})
				return
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Logger.Error("查询用户失败", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"message": "服务器错误"})
			return
		}

		// 6. 密码加密
		hashedPassword, err := password.HashPassword(req.Password)
		if err != nil {
			logger.Logger.Error("密码加密失败", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"message": "服务器错误"})
			return
		}

		// 7. 创建用户
		user := model.User{
			Username: req.Username,
			Password: hashedPassword,
			Status:   1,
		}
		if req.Email != "" {
			user.Email = &req.Email
		}
		if req.Phone != "" {
			user.Phone = &req.Phone
		}
		if req.Avatar != "" {
			user.Avatar = &req.Avatar
		}

		// 8. 插入数据库
		if err := db.Create(&user).Error; err != nil {
			logger.Logger.Error("创建用户失败", zap.Error(err), zap.String("username", req.Username))
			c.JSON(http.StatusInternalServerError, gin.H{"message": "注册失败"})
			return
		}

		logger.Logger.Info("用户注册成功",
			zap.Uint("user_id", user.ID),
			zap.String("username", user.Username),
			zap.String("ip", c.ClientIP()),
		)

		c.JSON(http.StatusOK, gin.H{
			"message": "注册成功",
			"user": gin.H{
				"id":         user.ID,
				"username":   user.Username,
				"email":      user.Email,
				"phone":      user.Phone,
				"avatar":     user.Avatar,
				"status":     user.Status,
				"created_at": user.CreatedAt,
			},
		})
	}
}
