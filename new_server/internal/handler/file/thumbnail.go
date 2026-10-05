package file

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/internal/thumb"
)

// thumbRequestTimeout 请求时当场生成的上限。正常几十到几百毫秒；超大图或机器忙时才会接近它。
const thumbRequestTimeout = 30 * time.Second

// HandlerFuncThumbnail 图片缩略图：GET /v1/file/thumbnail?path=&name=&w=256
//
// path/name 与 /file/download 同义（path 可以是目录，也可以已是完整路径）；w 是长边上限，
// 只认 128 / 256 / 512，其它值归到最近一档，不传按服务端默认。
// 外网/流量下的列表先用它，点进详情才走 /file/download 拉原图。
//
// 与 download 一样是二进制流端点，错误必须用真实 HTTP 状态码：按项目惯例回 200+JSON 的话，
// 客户端图片加载器会把那段 JSON 当图片字节去解码。客户端遇到非 200 应当回退到原图或占位图。
func HandlerFuncThumbnail(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims, ok := c.Get("UserInfo"); !ok || claims == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}

		path := c.Query("path")
		name := c.Query("name")
		if path == "" || name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少必要参数 path 或 name", "data": nil})
			return
		}
		width, _ := strconv.Atoi(c.Query("w"))

		// 与 download 一致：name 已是 path 末段则 path 即完整路径，否则 path 是目录
		var fullPath string
		if filepath.Base(path) == name {
			fullPath = path
		} else {
			fullPath = filepath.Join(path, name)
		}
		if !isPathAllowedDownload(fullPath) {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "无权访问该路径", "data": nil})
			return
		}

		svc := thumb.Global
		if svc == nil || !thumb.Supported(fullPath) {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"code": 415, "message": "该文件不支持缩略图", "data": nil})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), thumbRequestTimeout)
		defer cancel()
		out, err := svc.Get(ctx, fullPath, width)
		if err != nil {
			switch {
			case errors.Is(err, os.ErrNotExist):
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "文件不存在", "data": nil})
			case errors.Is(err, thumb.ErrUnsupported):
				c.JSON(http.StatusUnsupportedMediaType, gin.H{"code": 415, "message": "该文件不支持缩略图", "data": nil})
			case errors.Is(err, context.DeadlineExceeded):
				c.JSON(http.StatusGatewayTimeout, gin.H{"code": 504, "message": "缩略图生成超时", "data": nil})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "缩略图生成失败", "data": nil})
			}
			return
		}

		// 缓存文件名里已含「源路径+修改时间+大小+宽度」的指纹，源文件变了名字就变，正好当 ETag。
		// 客户端（Coil 等）据此直接用本地缓存，不再发请求或只发一次条件请求
		etag := `"` + strings.TrimSuffix(filepath.Base(out), ".jpg") + `"`
		c.Header("ETag", etag)
		c.Header("Cache-Control", "private, max-age=86400")
		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("Content-Type", "image/jpeg")
		c.File(out)
	}
}
