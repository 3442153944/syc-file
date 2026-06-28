package file

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/config"
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
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少必要参数 path 或 name", "data": nil})
			return
		}

		fullPath := filepath.Join(req.Path, req.Name)
		if !config.Conf.IsPathAllowed(fullPath) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "路径不被允许", "data": nil})
			return
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在", "data": nil})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "读取文件失败", "data": nil})
			return
		}
		if info.IsDir() {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "不支持删除目录", "data": nil})
			return
		}

		if err := os.Remove(fullPath); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "删除失败: " + err.Error(), "data": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": nil})
	}
}
