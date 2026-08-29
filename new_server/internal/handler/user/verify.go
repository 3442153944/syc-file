package user

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"net/http"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

func HandlerFuncVerify(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// token已经由AuthToken中间件解析并注入，直接取
		auth, _ := c.Get("Auth")
		if auth == false {
			c.JSON(http.StatusOK, gin.H{
				"code":    401,
				"message": "token无效或已过期",
				"data":    nil,
			})
			return
		}

		claims := c.MustGet("UserInfo").(*token.Claims)

		// 查询最新用户信息
		var u model.User
		if err := db.First(&u, claims.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, gin.H{
					"code":    401,
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

		if u.Status == 0 {
			c.JSON(http.StatusOK, gin.H{
				"code":    403,
				"message": "账号已被禁用",
				"data":    nil,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "ok",
			"data": gin.H{
				"id":       u.ID,
				"username": u.Username,
				"email":    u.Email,
				"phone":    u.Phone,
				"avatar":   u.Avatar,
				"role":     u.Role,
				"status":   u.Status,
				// 同 userLogin：个人中心的「最后登录 / 注册时间」靠这两个字段，缺了就只能显示未知
				"last_login":                 u.LastLogin,
				"created_at":                 u.CreatedAt,
				"quick_share_hotkey":         u.QuickShareHotkey,
				"quick_share_expire_minutes": u.QuickShareExpireMinutes,
			},
		})
	}
}
