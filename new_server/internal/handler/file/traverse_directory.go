package file

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/pkg/logger"
)

type fileItem struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	IsDir         bool      `json:"is_dir"`
	Size          int64     `json:"size"`
	ModTime       time.Time `json:"mod_time"`
	Mode          string    `json:"mode"`
	Extension     string    `json:"extension"`
	ChildrenCount int       `json:"children_count"`
}

type traverseResponse struct {
	CurrentPath string     `json:"current_path"`
	ParentPath  string     `json:"parent_path"`
	Items       []fileItem `json:"items"`
	TotalCount  int        `json:"total_count"`
	DirCount    int        `json:"dir_count"`
	FileCount   int        `json:"file_count"`
}

func HandlerFuncTraverseDirectory(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Path     string `json:"path" binding:"required"`
			Page     int    `json:"page"`
			PageSize int    `json:"page_size"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误", "data": nil})
			return
		}

		req.Path = filepath.Clean(req.Path)
		if len(req.Path) == 2 && req.Path[1] == ':' {
			req.Path = req.Path + string(filepath.Separator)
		}

		fileInfo, err := os.Stat(req.Path)
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "目录不存在", "data": nil})
			return
		}
		if err != nil {
			logger.Logger.Error("无法访问目录", zap.String("path", req.Path), zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "无法访问目录", "data": nil})
			return
		}
		if !fileInfo.IsDir() {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "路径不是目录", "data": nil})
			return
		}

		result, err := traverseSingleLevel(req.Path)
		if err != nil {
			logger.Logger.Error("遍历目录失败", zap.String("path", req.Path), zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "遍历目录失败", "data": nil})
			return
		}

		if req.Page > 0 && req.PageSize > 0 {
			start := (req.Page - 1) * req.PageSize
			end := req.Page * req.PageSize
			if start > len(result.Items) {
				start = len(result.Items)
			}
			if end > len(result.Items) {
				end = len(result.Items)
			}
			pagedItems := result.Items[start:end]
			totalPages := (result.TotalCount + req.PageSize - 1) / req.PageSize

			c.JSON(http.StatusOK, gin.H{
				"code":    200,
				"message": "ok",
				"data": gin.H{
					"current_path": result.CurrentPath,
					"parent_path":  result.ParentPath,
					"items":        pagedItems,
					"total_count":  result.TotalCount,
					"dir_count":    result.DirCount,
					"file_count":   result.FileCount,
					"pagination": gin.H{
						"page":        req.Page,
						"page_size":   req.PageSize,
						"total_pages": totalPages,
						"has_more":    req.Page < totalPages,
					},
				},
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": result})
	}
}

func traverseSingleLevel(path string) (*traverseResponse, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	logger.Logger.Info("读取目录", zap.String("path", path), zap.Int("entries_count", len(entries)))

	var items []fileItem
	var dirCount, fileCount int

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			logger.Logger.Warn("无法获取文件信息", zap.String("name", entry.Name()), zap.Error(err))
			continue
		}

		fullPath := filepath.Join(path, entry.Name())
		item := fileItem{
			Name:    entry.Name(),
			Path:    fullPath,
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode().String(),
		}

		if item.IsDir {
			dirCount++
			subEntries, err := os.ReadDir(fullPath)
			if err == nil {
				item.ChildrenCount = len(subEntries)
			}
		} else {
			fileCount++
			item.Extension = strings.ToLower(filepath.Ext(item.Name))
			if item.Extension == "" {
				item.Extension = "unknown"
			}
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir && !items[j].IsDir {
			return true
		}
		if !items[i].IsDir && items[j].IsDir {
			return false
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	res := &traverseResponse{
		CurrentPath: path,
		Items:       items,
		TotalCount:  len(items),
		DirCount:    dirCount,
		FileCount:   fileCount,
	}

	volName := fileVolumeName(path)
	if volName == "" {
		volName = "/"
	} else {
		volName = volName + string(filepath.Separator)
	}
	if path != volName && path != "/" && path != "." {
		res.ParentPath = filepath.Dir(path)
	}

	return res, nil
}
