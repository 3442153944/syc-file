package user

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"net/http"
	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/password"
	"syc-file/pkg/token"
)

func HandlerFuncLogin(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Email    string `json:"email"`
			Phone    string `json:"phone"`
			Password string `json:"password" binding:"required"`
		}

		// 1. 绑定参数
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"code":    400,
				"message": "参数格式错误",
				"data":    nil,
			})
			return
		}

		// 2. 至少一个登录凭证
		if req.Username == "" && req.Email == "" && req.Phone == "" {
			c.JSON(http.StatusOK, gin.H{
				"code":    400,
				"message": "请提供用户名、邮箱或手机号",
				"data":    nil,
			})
			return
		}

		// 3. 查找用户
		var u model.User
		query := db
		if req.Username != "" {
			query = query.Where("username = ?", req.Username)
		} else if req.Email != "" {
			query = query.Where("email = ?", req.Email)
		} else {
			query = query.Where("phone = ?", req.Phone)
		}

		if err := query.First(&u).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, gin.H{
					"code":    400,
					"message": "用户不存在",
					"data":    nil,
				})
				return
			}
			logger.Logger.Error("查询用户失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{
				"code":    500,
				"message": "服务器错误",
				"data":    nil,
			})
			return
		}

		// 4. 检查状态
		if u.Status == 0 {
			c.JSON(http.StatusOK, gin.H{
				"code":    403,
				"message": "账号已被禁用",
				"data":    nil,
			})
			return
		}

		// 5. 验证密码
		if !password.VerifyPassword(u.Password, req.Password) {
			c.JSON(http.StatusOK, gin.H{
				"code":    400,
				"message": "密码错误",
				"data":    nil,
			})
			return
		}

		// 6. 生成token
		tokenStr, err := token.GenerateToken(
			int64(u.ID),
			u.Username,
			stringVal(u.Email),
			[]string{u.Role},
			config.Conf.Auth.TokenExpire,
		)
		if err != nil {
			logger.Logger.Error("生成token失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{
				"code":    500,
				"message": "服务器错误",
				"data":    nil,
			})
			return
		}

		logger.Logger.Info("用户登录成功",
			zap.Int64("user_id", int64(u.ID)),
			zap.String("username", u.Username),
			zap.String("ip", c.ClientIP()),
		)

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "登录成功",
			"data": gin.H{
				"token": tokenStr,
				"user": gin.H{
					"id":       u.ID,
					"username": u.Username,
					"email":    u.Email,
					"phone":    u.Phone,
					"avatar":   u.Avatar,
					"role":     u.Role,
					"status":   u.Status,
					// 客户端个人中心要展示「最后登录 / 注册时间」，此前这两个字段没返回，
					// 三端一律显示「未知」。字段与 model.User 的 json tag 保持一致。
					"last_login": u.LastLogin,
					"created_at": u.CreatedAt,
					// 粘贴快传：桌面端登录后要立即知道这两个值才能注册全局快捷键 /
					// 决定分享有效期，不等用户手动打开设置页
					"quick_share_hotkey":         u.QuickShareHotkey,
					"quick_share_expire_minutes": u.QuickShareExpireMinutes,
				},
			},
		})
	}
}

// stringVal 指针转字符串
func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
