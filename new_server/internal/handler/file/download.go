package file

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syc-file/pkg/response"

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

func HandlerFuncDownload(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			response.WriteHTTP(c, http.StatusUnauthorized, 401, "请先登录", nil)
			return
		}
		userClaims := claims.(*token.Claims)
		userID := uint(userClaims.UserID)

		path := c.Query("path")
		name := c.Query("name")
		deviceIDStr := c.Query("device_id")

		// 注意：本端点是文件流，错误必须用真实 HTTP 状态码。
		// 若按项目惯例回 200+JSON 错误体，客户端会把这段 JSON 当文件字节落盘，
		// 表现为"下载成功但 hash 不匹配"（所有失败的 actual hash 相同），
		// 同步任务永远失败 + Reaper 无限重试。
		if path == "" || name == "" {
			response.WriteHTTP(c, http.StatusBadRequest, 400, "缺少必要参数 path 或 name", nil)
			return
		}

		var deviceID uint
		if deviceIDStr != "" {
			if id, err := strconv.ParseUint(deviceIDStr, 10, 32); err == nil {
				deviceID = uint(id)
			}
		}

		var fullPath string
		if filepath.Base(path) == name {
			fullPath = path
		} else {
			fullPath = filepath.Join(path, name)
		}

		var allowed bool
		fullPath, allowed = resolveFilePath(c, db, fullPath)
		if !allowed {
			return
		}

		fileInfo, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				response.WriteHTTP(c, http.StatusNotFound, 404, "文件不存在", nil)
			} else {
				logger.Logger.Error("获取文件信息失败", zap.Error(err), zap.String("path", fullPath))
				response.WriteHTTP(c, http.StatusInternalServerError, 500, "获取文件信息失败", nil)
			}
			return
		}

		if fileInfo.IsDir() {
			response.WriteHTTP(c, http.StatusBadRequest, 400, "不能下载目录", nil)
			return
		}

		fileSize := fileInfo.Size()

		history := &model.DownloadHistory{
			UserID:         userID,
			DeviceID:       deviceID,
			FileName:       &name,
			FileSize:       &fileSize,
			DownloadStatus: "pending",
		}
		ip := c.ClientIP()
		history.IPAddress = &ip
		db.Create(history)

		file, err := os.Open(fullPath)
		if err != nil {
			logger.Logger.Error("打开文件失败", zap.Error(err), zap.String("path", fullPath))
			response.WriteHTTP(c, http.StatusInternalServerError, 500, "打开文件失败", nil)
			return
		}
		defer file.Close()

		rangeHeader := c.GetHeader("Range")

		if rangeHeader == "" {
			serveFullFile(c, file, fileInfo, name, userID, history.ID, db)
		} else {
			serveRangeFile(c, file, fileSize, rangeHeader, name, userID, history.ID, db)
		}
	}
}

func serveFullFile(c *gin.Context, file *os.File, fileInfo os.FileInfo, fileName string, userID uint, historyID uint64, db *gorm.DB) {
	c.Header("Content-Type", getMimeType(fileName))
	c.Header("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	c.Header("Content-Disposition", "attachment; filename="+fileName)
	c.Header("Accept-Ranges", "bytes")
	c.Header("X-History-ID", strconv.FormatUint(historyID, 10))

	if historyID > 0 {
		db.Model(&model.DownloadHistory{}).Where("id = ?", historyID).Update("download_status", "downloading")
	}

	ws.SendToUser(userID, ws.MessageType("file_download"), gin.H{
		"event":      "start",
		"file_name":  fileName,
		"file_size":  fileInfo.Size(),
		"history_id": historyID,
	})

	c.Status(http.StatusOK)
	written, err := io.Copy(c.Writer, file)
	if err != nil {
		logger.Logger.Error("文件传输失败", zap.Error(err))
		if historyID > 0 {
			db.Model(&model.DownloadHistory{}).Where("id = ?", historyID).Update("download_status", "failed")
		}
		return
	}

	if historyID > 0 {
		db.Model(&model.DownloadHistory{}).Where("id = ?", historyID).Updates(map[string]interface{}{
			"download_status": "completed",
			"completed_at":    gorm.Expr("NOW()"),
		})
	}

	ws.SendToUser(userID, ws.MessageType("file_download"), gin.H{
		"event":      "completed",
		"file_name":  fileName,
		"file_size":  written,
		"history_id": historyID,
	})

	logger.Logger.Info("文件下载完成", zap.Uint("user_id", userID), zap.String("file_name", fileName), zap.Int64("file_size", written))
}

func serveRangeFile(c *gin.Context, file *os.File, fileSize int64, rangeHeader string, fileName string, userID uint, historyID uint64, db *gorm.DB) {
	ranges := strings.TrimPrefix(rangeHeader, "bytes=")
	parts := strings.Split(ranges, "-")
	if len(parts) != 2 {
		response.WriteHTTP(c, http.StatusOK, 400, "无效的 Range 请求", nil)
		return
	}

	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= fileSize {
		c.Header("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
		response.WriteHTTP(c, http.StatusOK, 416, "Range 超出范围", nil)
		return
	}

	var end int64
	if parts[1] == "" {
		end = fileSize - 1
	} else {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end >= fileSize {
			end = fileSize - 1
		}
	}

	if start > end {
		c.Header("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
		response.WriteHTTP(c, http.StatusOK, 416, "Range 起始位置大于结束位置", nil)
		return
	}

	contentLength := end - start + 1
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		logger.Logger.Error("文件定位失败", zap.Error(err))
		response.WriteHTTP(c, http.StatusOK, 500, "文件定位失败", nil)
		return
	}

	c.Header("Content-Type", getMimeType(fileName))
	c.Header("Content-Length", strconv.FormatInt(contentLength, 10))
	c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
	c.Header("Content-Disposition", "attachment; filename="+fileName)
	c.Header("Accept-Ranges", "bytes")
	c.Header("X-History-ID", strconv.FormatUint(historyID, 10))

	if historyID > 0 {
		db.Model(&model.DownloadHistory{}).Where("id = ?", historyID).Update("download_status", "downloading")
	}

	c.Status(http.StatusPartialContent)
	written, err := io.CopyN(c.Writer, file, contentLength)
	if err != nil && err != io.EOF {
		logger.Logger.Error("文件传输失败", zap.Error(err))
		return
	}

	if end == fileSize-1 && historyID > 0 {
		db.Model(&model.DownloadHistory{}).Where("id = ?", historyID).Updates(map[string]interface{}{
			"download_status": "completed",
			"completed_at":    gorm.Expr("NOW()"),
		})
		err := ws.SendToUser(userID, ws.MessageType("file_download"), gin.H{
			"event":      "completed",
			"file_name":  fileName,
			"file_size":  fileSize,
			"history_id": historyID,
		})
		if err != nil {
			logger.Logger.Error("发送 Range 下载完成消息失败", zap.Error(err))
			response.WriteHTTP(c, http.StatusOK, 500, "服务器异常，发送 Range 下载完成消息失败", "failed")
			return
		}
	}

	logger.Logger.Info("Range 下载完成", zap.Uint("user_id", userID), zap.String("file_name", fileName), zap.Int64("start", start), zap.Int64("end", end), zap.Int64("written", written))
}

func isPathAllowedDownload(path string) bool {
	return config.Conf.IsPathAllowed(path)
}

func getMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	mimeTypes := map[string]string{
		".txt": "text/plain", ".pdf": "application/pdf",
		".zip": "application/zip", ".rar": "application/x-rar-compressed",
		".7z":  "application/x-7z-compressed",
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".gif": "image/gif", ".mp4": "video/mp4", ".mp3": "audio/mpeg",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}
