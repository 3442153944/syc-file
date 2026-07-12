package user

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
	"syc-file/pkg/token"
)

func HandlerFuncChangePassword(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userClaims := claims.(*token.Claims)
		userID := uint(userClaims.UserID)

		var req struct {
			OldPassword string `json:"old_password" binding:"required"`
			NewPassword string `json:"new_password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数格式错误", "data": nil})
			return
		}

		if len(req.NewPassword) < 6 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "新密码长度至少6位", "data": nil})
			return
		}

		if req.OldPassword == req.NewPassword {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "新密码不能与旧密码相同", "data": nil})
			return
		}

		var u model.User
		if err := db.First(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "用户不存在", "data": nil})
				return
			}
			logger.Logger.Error("查询用户失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "服务器错误", "data": nil})
			return
		}

		if u.Status == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "账号已被禁用", "data": nil})
			return
		}

		if !password.VerifyPassword(u.Password, req.OldPassword) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "旧密码错误", "data": nil})
			return
		}

		hashedPassword, err := password.HashPassword(req.NewPassword)
		if err != nil {
			logger.Logger.Error("密码加密失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "服务器错误", "data": nil})
			return
		}

		if err := db.Model(&u).Update("password", hashedPassword).Error; err != nil {
			logger.Logger.Error("更新密码失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "服务器错误", "data": nil})
			return
		}

		logger.Logger.Info("用户修改密码成功",
			zap.Uint("user_id", u.ID),
			zap.String("username", u.Username),
			zap.String("ip", c.ClientIP()),
		)

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "密码修改成功", "data": nil})
	}
}
