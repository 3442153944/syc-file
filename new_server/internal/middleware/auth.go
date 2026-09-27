package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"
	"strings"
	"syc-file/config"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
	"time"
)

// CORS 跨域中间件
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Token, Device-Id")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// isWhitelisted 判断请求路径是否命中配置文件中的白名单
// 支持精确匹配，或以白名单项为前缀的子路径匹配（如 /v1/public 放行 /v1/public/xxx）
func isWhitelisted(path string) bool {
	for _, p := range config.Conf.Whitelist {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// Auth 统一认证中间件
// 是否需要登录完全由配置文件 whitelist 决定：白名单内的路由直接放行，
// 其余路由必须携带有效 token，否则返回 401。无需再手动区分 public/private 路由组。
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("Auth", false)

		// 尽量解析 token 并注入用户信息（白名单路由如 /user/verify 也依赖此信息）
		tokenStr := c.GetHeader("Token")
		if tokenStr == "" {
			tokenStr = c.Query("token")
		}
		// 设备绑定：REST 走 Device-Id 头，WS 握手（/v1/ws/connect）没法带自定义头，
		// 走它已有的 device_id query 参数
		deviceID := c.GetHeader("Device-Id")
		if deviceID == "" {
			deviceID = c.Query("device_id")
		}
		if tokenStr != "" {
			if claims, err := token.ParseToken(tokenStr); err != nil {
				logger.Logger.Warn("Token验证失败", zap.Error(err))
			} else if claims.DeviceID == "" || claims.DeviceID != deviceID {
				// 旧版签发（无 device_id）或者换了设备重放，一律当未登录处理，
				// 逼一次重新登录来补上绑定关系
				logger.Logger.Warn("Token设备不匹配，拒绝",
					zap.Int64("user_id", claims.UserID),
					zap.String("bound_device", claims.DeviceID),
					zap.String("request_device", deviceID),
				)
			} else {
				// 检查剩余有效期，不足 refresh_expire 天则自动刷新
				remaining := time.Until(claims.ExpiresAt.Time)
				refreshThreshold := time.Duration(config.Conf.Auth.RefreshExpire) * 24 * time.Hour
				if remaining < refreshThreshold {
					newToken, err := token.GenerateToken(
						claims.UserID,
						claims.Username,
						claims.Email,
						claims.Roles,
						claims.DeviceID,
						config.Conf.Auth.TokenExpire,
					)
					if err != nil {
						logger.Logger.Warn("Token刷新失败", zap.Error(err))
					} else {
						// 新token写回响应头，前端从 New-Token 取
						c.Header("New-Token", newToken)
						c.Header("Token-Refreshed", "true")
						logger.Logger.Info("Token已自动刷新",
							zap.Int64("user_id", claims.UserID),
							zap.String("username", claims.Username),
						)
					}
				}

				c.Set("Auth", true)
				c.Set("UserInfo", claims)
				logger.Logger.Info("Token验证成功",
					zap.Int64("user_id", claims.UserID),
					zap.String("username", claims.Username),
				)
			}
		}

		// 白名单路由直接放行，无需登录
		if isWhitelisted(c.Request.URL.Path) {
			c.Next()
			return
		}

		// 非白名单路由必须已认证
		if auth, _ := c.Get("Auth"); auth != true {
			c.JSON(http.StatusOK, gin.H{
				"code":    401,
				"message": "未登录，请先登录",
				"data":    nil,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireRole 角色校验
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userInfo, exists := c.Get("UserInfo")
		if !exists {
			c.JSON(http.StatusOK, gin.H{
				"code":    401,
				"message": "未登录",
				"data":    nil,
			})
			c.Abort()
			return
		}

		claims := userInfo.(*token.Claims)
		for _, required := range roles {
			for _, role := range claims.Roles {
				if role == required {
					c.Next()
					return
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    403,
			"message": "权限不足",
			"data":    nil,
		})
		c.Abort()
	}
}
