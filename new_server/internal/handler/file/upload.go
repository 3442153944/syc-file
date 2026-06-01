package file

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

func HandlerFuncUpload(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userClaims := claims.(*token.Claims)
		userID := uint(userClaims.UserID)

		var req struct {
			Path   string `json:"path" binding:"required"`
			Name   string `json:"name" binding:"required"`
			Action string `json:"action" binding:"required"`
		}

		contentType := c.GetHeader("Content-Type")
		if strings.HasPrefix(contentType, "multipart/form-data") {
			req.Path = c.PostForm("path")
			req.Name = c.PostForm("name")
			req.Action = c.PostForm("action")
		} else {
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数解析失败", "data": nil})
				return
			}
		}

		if req.Path == "" || req.Name == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少必要参数 path 或 name", "data": nil})
			return
		}
		if req.Action != "check" && req.Action != "upload" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "无效的 action 参数（可选: check / upload）", "data": nil})
			return
		}

		logger.Logger.Info("收到上传请求", zap.String("path", req.Path), zap.String("name", req.Name), zap.String("action", req.Action), zap.Uint("user_id", userID))

		normalizedPath := filepath.FromSlash(req.Path)
		var fullPath string
		if filepath.Base(normalizedPath) == req.Name {
			fullPath = normalizedPath
		} else {
			fullPath = filepath.Join(normalizedPath, req.Name)
		}

		if !isPathAllowedDownload(fullPath) {
			logger.Logger.Warn("上传路径访问被拒绝", zap.Uint("user_id", userID), zap.String("path", fullPath))
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权访问该路径", "data": nil})
			return
		}

		ext := filepath.Ext(req.Name)
		if !config.Conf.IsExtensionAllowed(ext) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "不允许上传该类型的文件", "data": nil})
			return
		}

		if len(req.Name) > config.Conf.File.Upload.MaxFilenameLength {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": fmt.Sprintf("文件名过长（最大 %d 字符）", config.Conf.File.Upload.MaxFilenameLength), "data": nil})
			return
		}

		switch req.Action {
		case "check":
			handleCheck(c, fullPath, req.Name)
		case "upload":
			handleUpload(c, db, fullPath, req.Name, userID)
		}
	}
}

func handleCheck(c *gin.Context, fullPath, fileName string) {
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{
				"code":    200,
				"message": "ok",
				"data": gin.H{
					"exists":     false,
					"can_upload": true,
					"file_name":  fileName,
					"path":       fullPath,
				},
			})
		} else {
			logger.Logger.Error("检查文件状态失败", zap.Error(err), zap.String("path", fullPath))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "检查文件状态失败", "data": nil})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data": gin.H{
			"exists":      true,
			"can_upload":  false,
			"file_name":   fileName,
			"file_size":   fileInfo.Size(),
			"path":        fullPath,
			"modified_at": fileInfo.ModTime(),
		},
	})
}

func handleUpload(c *gin.Context, db *gorm.DB, fullPath, fileName string, userID uint) {
	if _, err := os.Stat(fullPath); err == nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "文件已存在，请先删除或重命名", "data": nil})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		logger.Logger.Error("获取上传文件失败", zap.Error(err), zap.Uint("user_id", userID))
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": "请选择要上传的文件", "data": nil})
		return
	}

	if fileHeader.Size > config.Conf.File.Upload.MaxFileSize {
		c.JSON(http.StatusOK, gin.H{"code": 400, "message": fmt.Sprintf("文件大小超过限制（最大 %d MB）", config.Conf.File.Upload.MaxFileSize/1024/1024), "data": nil})
		return
	}

	targetDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		logger.Logger.Error("创建目标目录失败", zap.Error(err), zap.String("path", targetDir))
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": "创建目标目录失败", "data": nil})
		return
	}

	ip := c.ClientIP()
	ua := c.GetHeader("User-Agent")
	fileType := fileHeader.Header.Get("Content-Type")
	history := &model.UploadHistory{
		UserID:       userID,
		FileName:     fileName,
		OriginalName: &fileHeader.Filename,
		FileSize:     &fileHeader.Size,
		FileType:     &fileType,
		StoragePath:  &fullPath,
		UploadStatus: "uploading",
		IPAddress:    &ip,
		UserAgent:    &ua,
	}
	db.Create(history)

	ws.SendToUser(userID, ws.MessageType("file_upload"), gin.H{
		"event":        "start",
		"file_name":    fileName,
		"file_size":    fileHeader.Size,
		"history_id":   history.ID,
		"storage_path": fullPath,
	})

	if err := c.SaveUploadedFile(fileHeader, fullPath); err != nil {
		logger.Logger.Error("保存文件失败", zap.Error(err), zap.String("path", fullPath))
		if history.ID > 0 {
			errMsg := "保存文件失败: " + err.Error()
			db.Model(history).Updates(map[string]interface{}{
				"upload_status": "failed",
				"error_message": errMsg,
			})
		}
		ws.SendToUser(userID, ws.MessageType("file_upload"), gin.H{
			"event":      "failed",
			"file_name":  fileName,
			"history_id": history.ID,
		})
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": "保存文件失败", "data": nil})
		return
	}

	if history.ID > 0 {
		db.Model(history).Updates(map[string]interface{}{
			"upload_status": "completed",
			"completed_at":  gorm.Expr("NOW()"),
		})
	}

	ws.SendToUser(userID, ws.MessageType("file_upload"), gin.H{
		"event":        "completed",
		"file_name":    fileName,
		"file_size":    fileHeader.Size,
		"history_id":   history.ID,
		"storage_path": fullPath,
	})

	logger.Logger.Info("文件上传完成", zap.Uint("user_id", userID), zap.String("file_name", fileName), zap.Int64("file_size", fileHeader.Size), zap.String("path", fullPath))

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "上传成功",
		"data": gin.H{
			"history_id":    history.ID,
			"file_name":     fileName,
			"original_name": fileHeader.Filename,
			"file_size":     fileHeader.Size,
			"storage_path":  fullPath,
		},
	})
}
