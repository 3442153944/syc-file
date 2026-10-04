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

// builtinWhitelist 代码内置的免登录路径，不依赖 config.yaml：
// 老部署的配置文件里没有这些条目，升级后也必须能用（否则未初始化 / 未登录的客户端无法探测服务器状态）。
var builtinWhitelist = []string{
	"/v1/system/status",
	"/v1/system/init",
	"/v1/routes",
}

// LiveUserLookup 查账号最新状态（角色 / 级别 / 启用）。由 system 包注入，避免 middleware 反向依赖。
type LiveUserLookup func(userID int64) (role string, level int8, status int8, ok bool)

// GuestGuard 游客请求放行判定（默认拒绝）。由 system 包注入。
type GuestGuard func(c *gin.Context, userID int64) bool

// GuestAlive 访客账号此刻是否仍有效（启用、未过期、仍是访客）。由 system 包注入。
type GuestAlive func(userID int64) bool

var (
	userLookup LiveUserLookup
	guestGuard GuestGuard
	guestAlive GuestAlive
)

func SetUserLookup(f LiveUserLookup) { userLookup = f }
func SetGuestGuard(f GuestGuard)     { guestGuard = f }
func SetGuestAlive(f GuestAlive)     { guestAlive = f }

// LevelOf 取当前请求的权限级别（未登录为 0）。
func LevelOf(c *gin.Context) int8 {
	if v, ok := c.Get("UserLevel"); ok {
		if l, ok := v.(int8); ok {
			return l
		}
	}
	return 0
}

// RequireLevel 要求权限级别不低于 min，否则 403。
func RequireLevel(min int8) gin.HandlerFunc {
	return func(c *gin.Context) {
		if auth, _ := c.Get("Auth"); auth != true {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未登录", "data": nil})
			c.Abort()
			return
		}
		if LevelOf(c) < min {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "权限不足", "data": nil})
			c.Abort()
			return
		}
		c.Next()
	}
}

// isWhitelisted 判断请求路径是否命中配置文件中的白名单
// 支持精确匹配，或以白名单项为前缀的子路径匹配（如 /v1/public 放行 /v1/public/xxx）
func isWhitelisted(path string) bool {
	for _, p := range builtinWhitelist {
		if path == p {
			return true
		}
	}
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
				level := claims.EffectiveLevel()
				remaining := time.Until(claims.ExpiresAt.Time)
				refreshThreshold := time.Duration(config.Conf.Auth.RefreshExpire) * 24 * time.Hour
				// 访客账号自带有效期，token 不续期（续了就活过账号了）
				if remaining < refreshThreshold && level > 0 {
					roles := claims.Roles
					canRefresh := true
					if userLookup != nil {
						// 续期时按库里最新的角色 / 级别重签：降级、禁用在这里生效，不会被旧 token 一直续下去
						if role, lv, status, ok := userLookup(claims.UserID); ok && status == 1 && lv > 0 {
							roles, level = []string{role}, lv
						} else {
							canRefresh = false
						}
					}
					var newToken string
					var err error
					if canRefresh {
						newToken, err = token.GenerateToken(
							claims.UserID,
							claims.Username,
							claims.Email,
							roles,
							claims.DeviceID,
							level,
							config.Conf.Auth.TokenExpire,
						)
					}
					if !canRefresh {
						// 账号已被禁用 / 删除：不续期，旧 token 自然到期
					} else if err != nil {
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

				// 访客账号被禁用 / 过期 / 删除后，手里的 token 虽然签名有效，也必须立刻失效：
				// 在这里（白名单判定之前）按未登录处理，白名单接口同样拿不到身份
				if level == 0 && (guestAlive == nil || !guestAlive(claims.UserID)) {
					logger.Logger.Warn("访客账号已失效，按未登录处理", zap.Int64("user_id", claims.UserID))
				} else {
					c.Set("Auth", true)
					c.Set("UserInfo", claims)
					c.Set("UserLevel", level)
				}
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

		// 游客（level=0）默认拒绝：只放行 system 包按「被授权页面」算出的接口
		if LevelOf(c) == 0 {
			uc, _ := c.Get("UserInfo")
			claims, _ := uc.(*token.Claims)
			if claims == nil || guestGuard == nil || !guestGuard(c, claims.UserID) {
				c.JSON(http.StatusOK, gin.H{"code": 403, "message": "访客无权访问该接口，或访客账号已过期", "data": nil})
				c.Abort()
				return
			}
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
