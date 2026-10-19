package file

import (
	"net/http"
	"os"
	"path/filepath"
	"syc-file/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// HandlerFuncDeleteFile 删除指定文件（文件管理用，非同步路径）。
// 仅允许删除位于 AllowedPaths 内的普通文件，拒绝目录，防止误删整目录。
func HandlerFuncDeleteFile(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" || req.Name == "" {
			response.WriteHTTP(c, http.StatusOK, 400, "缺少必要参数 path 或 name", nil)
			return
		}

		fullPath := filepath.Join(req.Path, req.Name)
		var allowed bool
		fullPath, allowed = resolveFilePath(c, db, fullPath)
		if !allowed {
			return
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				response.WriteHTTP(c, http.StatusOK, 404, "文件不存在", nil)
				return
			}
			response.WriteHTTP(c, http.StatusOK, 500, "读取文件失败", nil)
			return
		}
		if info.IsDir() {
			response.WriteHTTP(c, http.StatusOK, 400, "不支持删除目录", nil)
			return
		}

		if err := os.Remove(fullPath); err != nil {
			response.WriteHTTP(c, http.StatusOK, 500, "删除失败: "+err.Error(), nil)
			return
		}
		response.WriteHTTP(c, http.StatusOK, 200, "ok", nil)
	}
}
