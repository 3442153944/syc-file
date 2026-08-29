package file

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

const defaultQuickShareCapacityBytes int64 = 10 * 1024 * 1024 * 1024 // 10GB，config 没配时的兜底

// HandlerFuncQuickShareUpload 粘贴快传：单次原始字节 POST（不分片，量小求快），
// 按用户配额裁量：不超过 quick_share.memory_threshold_bytes 就整份放进 quickShareMemStore
// 模拟磁盘，超过则落盘到该用户专属的 quick_share.base_path/<user_id>/ 目录——不进普通
// 存储树、不建 model.File、不参与同步引擎。落地后立即调 finalizeShareLink 生成分享链接，
// 有效期取用户在用户中心配置的默认值，没配就用 config 的全局默认值。
func HandlerFuncQuickShareUpload(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		name := filepath.Base(c.Query("name"))
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = "pasted-file"
		}

		qs := config.Conf.QuickShare
		if qs.BasePath == "" {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "未配置粘贴快传目录 quick_share.base_path", "data": nil})
			return
		}
		maxCapacity := qs.MaxCapacityBytes
		if maxCapacity <= 0 {
			maxCapacity = defaultQuickShareCapacityBytes
		}

		used, err := quickShareUsedBytes(db, userID)
		if err != nil {
			logger.Logger.Error("查询快传配额失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询配额失败", "data": nil})
			return
		}
		remaining := maxCapacity - used
		if remaining <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "快速共享空间已满，请等待旧分享过期或前往分享管理提前清理", "data": nil})
			return
		}

		limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, remaining)

		tempName := uuid.NewString() + filepath.Ext(name)
		userDir := filepath.Join(qs.BasePath, strconv.FormatUint(uint64(userID), 10))
		diskPath := filepath.Join(userDir, tempName)

		data, onDisk, size, err := spillToDiskIfLarge(limitedBody, qs.MemoryThresholdBytes, func() (string, error) {
			if mkErr := os.MkdirAll(userDir, 0o755); mkErr != nil {
				return "", mkErr
			}
			return diskPath, nil
		})
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "超出剩余配额", "data": nil})
				return
			}
			logger.Logger.Error("读取粘贴内容失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "读取内容失败", "data": nil})
			return
		}
		if size == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "内容为空", "data": nil})
			return
		}

		storageKind := "memory"
		originalPath := "quickshare://memory"
		if onDisk {
			storageKind = "disk"
			originalPath = diskPath
		}

		expireMinutes := quickShareExpireMinutesFor(db, userID)

		shareCode, respData, err := finalizeShareLink(db, redisClient, userID, originalPath, name, tempName,
			storageKind, size, expireMinutes, false, true)
		if err != nil {
			if onDisk {
				_ = os.Remove(diskPath)
			}
			logger.Logger.Error("写入快传分享记录失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "写入分享记录失败", "data": nil})
			return
		}
		if !onDisk {
			quickShareMemStore.Store(shareCode, data)
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": respData})
	}
}

// HandlerFuncQuickShareQuota 返回当前用户粘贴快传的配额使用情况，供用户中心展示。
func HandlerFuncQuickShareQuota(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		used, err := quickShareUsedBytes(db, userID)
		if err != nil {
			logger.Logger.Error("查询快传配额失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询配额失败", "data": nil})
			return
		}
		maxCapacity := config.Conf.QuickShare.MaxCapacityBytes
		if maxCapacity <= 0 {
			maxCapacity = defaultQuickShareCapacityBytes
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "ok",
			"data": gin.H{
				"used_bytes": used,
				"max_bytes":  maxCapacity,
			},
		})
	}
}

// quickShareUsedBytes 该用户当前有效（未过期/未吊销）的粘贴快传内容总大小——实时聚合，
// 不用维护一个额外的计数字段：进程重启、异常销毁都不会导致计数漂移，代价是每次上传/
// 查配额多一次简单聚合查询，个人使用场景数据量小，可以忽略。
func quickShareUsedBytes(db *gorm.DB, userID uint) (int64, error) {
	var used int64
	err := db.Model(&model.ShareLink{}).
		Where("user_id = ? AND status = ? AND is_quick_share = ?", userID, shareStatusActive, true).
		Select("COALESCE(SUM(file_size),0)").Scan(&used).Error
	return used, err
}

// quickShareExpireMinutesFor 用户在用户中心配置的默认有效期优先，没配就用全局默认值。
func quickShareExpireMinutesFor(db *gorm.DB, userID uint) int {
	fallback := config.Conf.QuickShare.DefaultExpireMinutes
	if fallback <= 0 {
		fallback = 60
	}
	var u model.User
	if err := db.Select("quick_share_expire_minutes").First(&u, userID).Error; err != nil {
		return fallback
	}
	if u.QuickShareExpireMinutes != nil && *u.QuickShareExpireMinutes > 0 {
		return *u.QuickShareExpireMinutes
	}
	return fallback
}

// spillToDiskIfLarge 读取 r：不超过 memThreshold 字节就整个读进内存返回；一旦超过，
// 把已经读到的前缀 + 剩余的流写到 diskPathFn() 给出的路径上。峰值内存恒定为
// memThreshold+1 字节左右，不随最终文件大小增长。diskPathFn 只在真的需要落盘时才调用
// （负责创建目录、给出目标路径），避免小文件走内存路径时白白 MkdirAll。
func spillToDiskIfLarge(r io.Reader, memThreshold int64, diskPathFn func() (string, error)) (data []byte, onDisk bool, size int64, err error) {
	if memThreshold < 0 {
		memThreshold = 0
	}
	limited := io.LimitReader(r, memThreshold+1)
	buf, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, false, 0, readErr
	}
	if int64(len(buf)) <= memThreshold {
		return buf, false, int64(len(buf)), nil
	}

	diskPath, pathErr := diskPathFn()
	if pathErr != nil {
		return nil, false, 0, pathErr
	}
	f, createErr := os.Create(diskPath)
	if createErr != nil {
		return nil, false, 0, createErr
	}
	defer f.Close()
	n1, writeErr := f.Write(buf)
	if writeErr != nil {
		return nil, false, 0, writeErr
	}
	n2, copyErr := io.Copy(f, r)
	if copyErr != nil {
		return nil, false, 0, copyErr
	}
	return nil, true, int64(n1) + n2, nil
}
