// Package admin 管理端接口：用户 / 设备 / 操作日志 / 存储配额 / 角色权限。
//
// 这些表（user_role、role、permission、device、operation_log、storage_config）建库时就有，
// 但一直没有任何业务代码，桌面端「管理」相关页面因此无数据可用。本包补齐它们。
//
// 约定：
//   - 一律走 v1 认证组，写操作全部 requireAdmin；
//   - 响应统一 {code,message,data}，分页统一 {list,total,page,page_size}（与 sync 域一致）；
//   - HTTP 状态码一律 200，业务码放 body（与全站既有约定保持一致，客户端按 body.code 判断）。
package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/pkg/token"
)

// APIHandler 管理域处理器。
type APIHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewAPIHandler(db *gorm.DB, redisClient *redis.Client) *APIHandler {
	return &APIHandler{db: db, redisClient: redisClient}
}

// pageQuery 统一的分页参数：page 从 1 开始，page_size 默认 20、上限 200。
type pageQuery struct {
	Page     int
	PageSize int
}

func parsePage(c *gin.Context) pageQuery {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return pageQuery{Page: page, PageSize: size}
}

func (p pageQuery) offset() int { return (p.Page - 1) * p.PageSize }

func jsonPage(c *gin.Context, list interface{}, total int64, p pageQuery) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
		"list": list, "total": total, "page": p.Page, "page_size": p.PageSize,
	}})
}

func jsonOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": data})
}

func jsonErr(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg, "data": nil})
}

// currentUser 取当前登录用户 ID。
func currentUser(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		jsonErr(c, 401, "未授权")
		return 0, false
	}
	return uint(claimsAny.(*token.Claims).UserID), true
}

// requireAdmin 仅管理员可继续。判据是 token 里的 roles（登录时按 user.role 签发）。
func requireAdmin(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		jsonErr(c, 401, "未授权")
		return 0, false
	}
	claims := claimsAny.(*token.Claims)
	for _, r := range claims.Roles {
		if r == "admin" {
			return uint(claims.UserID), true
		}
	}
	jsonErr(c, 403, "权限不足，仅管理员可操作")
	return 0, false
}

// hasAdminRole 只判断是不是管理员，不写响应（用于「本人或管理员」这类分支）。
func hasAdminRole(c *gin.Context) bool {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		return false
	}
	for _, r := range claimsAny.(*token.Claims).Roles {
		if r == "admin" {
			return true
		}
	}
	return false
}

// idParam 解析路径上的 :id。
func idParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		jsonErr(c, 400, "无效的 ID")
		return 0, false
	}
	return id, true
}
