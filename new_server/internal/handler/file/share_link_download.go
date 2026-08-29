package file

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/internal/model"
)

// shareLinkMeta 对应创建时写入 Redis 的 JSON 结构（见 share_link_janitor.go 的 finalizeShareLink），
// 下载热路径优先读它：命中即代表未过期（TTL 到点 key 自动消失），不用每次都查 MySQL。
type shareLinkMeta struct {
	TempName     string `json:"temp_name"`
	FileName     string `json:"file_name"`
	IsQuickShare bool   `json:"is_quick_share"`
	StorageKind  string `json:"storage_kind"`
	UserID       uint   `json:"user_id"`
}

// shareViaNginxHeader 由 nginx 的 /file/ 反代 location 用 proxy_set_header 强制设置（覆盖任何客户端
// 自带的同名头），用来告诉 Go 这个请求确实经过了 nginx。之所以不用一个全局配置开关，是因为同一个 Go
// 进程要同时服务两类客户端：网页端走 nginx（可以用 X-Accel-Redirect），Tauri 桌面端直连 8991 端口
// （没有 nginx 帮忙解释这个头，只能 Go 自己吐文件）——全局开关没法同时满足这两种场景，只能按请求判断。
const shareViaNginxHeader = "X-Via-Nginx"

// HandlerFuncShareDownload 分享链接的"阳"入口：公开路由（在 whitelist 里放行），免登录，
// 但每一次访问都会重新校验有效期/状态，校验通过后分三路决定怎么把字节吐给客户端：
//   - 内存态（粘贴快传的小文件）：nginx 摸不到 Go 进程内存，永远由 Go 自己从 quickShareMemStore
//     里取字节直接吐，完全不看 X-Via-Nginx 头。
//   - 磁盘态且请求带着 nginx 打上的 X-Via-Nginx 头：只设置 X-Accel-Redirect 头，由 nginx 的
//     internal location 用 sendfile 直接吐文件，Go 进程完全不碰文件内容；之后每次点开这个
//     分享链接，请求依旧会先回到这里过一遍鉴权，不会绕过校验。
//   - 磁盘态且没有该头（Tauri 桌面端直连本服务 / 没有 nginx 的开发环境 / 粘贴快传落盘的情况，
//     图省事没有单独接一条 nginx internal location）：退化为 Go 自己流式吐文件。
func HandlerFuncShareDownload(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		if code == "" {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "链接不存在", "data": nil})
			return
		}

		link, ok := lookupShareLink(db, redisClient, code)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "链接不存在或已过期", "data": nil})
			return
		}

		c.Header("Content-Disposition", "attachment; filename="+link.FileName)
		c.Header("Content-Type", getMimeType(link.FileName))
		// 分享内容会到期失效，中间的 CDN/浏览器都不应该替我们缓存过期前的响应。
		c.Header("Cache-Control", "no-store")

		if link.StorageKind == "memory" {
			data, found := quickShareMemStore.Load(code)
			if !found {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "文件不存在或已被清理", "data": nil})
				return
			}
			c.Data(http.StatusOK, getMimeType(link.FileName), data.([]byte))
			return
		}

		tempPath := resolveShareTempPath(link)
		if _, err := os.Stat(tempPath); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "文件不存在或已被清理", "data": nil})
			return
		}

		if c.GetHeader(shareViaNginxHeader) != "" && !link.IsQuickShare {
			c.Header("X-Accel-Redirect", "/internal-temp/"+link.TempName)
			c.Status(http.StatusOK)
			return
		}

		c.File(tempPath)
	}
}

// lookupShareLink 优先查 Redis：命中即代表分享仍在有效期内（key 由创建时按有效期设置 TTL，到期自动消失）。
// 未命中再退回 MySQL 兜底（Redis 重启丢数据、或分享刚好在这次请求和后台清理协程之间的极短窗口过期），
// 并顺带做一次即时过期判断——避免完全依赖后台协程/兜底 janitor 的清理时机，导致过期后仍可下载的窗口期。
func lookupShareLink(db *gorm.DB, redisClient *redis.Client, code string) (model.ShareLink, bool) {
	if redisClient != nil {
		if data, err := redisClient.Get(context.Background(), shareLinkRedisPrefix+code).Bytes(); err == nil {
			var meta shareLinkMeta
			if json.Unmarshal(data, &meta) == nil && meta.TempName != "" {
				return model.ShareLink{
					ShareCode:    code,
					UserID:       meta.UserID,
					FileName:     meta.FileName,
					TempName:     meta.TempName,
					IsQuickShare: meta.IsQuickShare,
					StorageKind:  meta.StorageKind,
				}, true
			}
		}
	}

	var link model.ShareLink
	if err := db.Where("share_code = ? AND status = 1", code).First(&link).Error; err != nil {
		return model.ShareLink{}, false
	}
	if !time.Now().Before(link.ExpireTime) {
		go expireShareLink(db, redisClient, link.ShareCode, resolveShareTempPath(link))
		return model.ShareLink{}, false
	}
	return link, true
}
