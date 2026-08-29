package file

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

const shareLinkRedisPrefix = "share:link:"

// HandlerFuncCreateShareLink 创建分享链接：
// 文件——与 temp 目录同卷则硬链接，跨卷则复制到 temp；
// 目录——自动打包为 zip（不压缩，Store 模式）落到 temp；
// 元信息落库 share_link 表 + Redis（TTL=有效期），并启动独立协程管理该链接生命周期，到期自毁。
func HandlerFuncCreateShareLink(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		var req struct {
			Path          string `json:"path"`
			Name          string `json:"name"`
			ExpireMinutes int    `json:"expire_minutes"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" || req.Name == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少必要参数 path 或 name", "data": nil})
			return
		}

		maxExpire := config.Conf.Share.MaxExpireMinutes
		if maxExpire <= 0 {
			maxExpire = 30 * 24 * 60
		}
		if req.ExpireMinutes <= 0 || req.ExpireMinutes > maxExpire {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "有效期不合法", "data": nil})
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

		tempDir := config.Conf.Share.TempPath
		if tempDir == "" {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "未配置分享临时目录 share.temp_path", "data": nil})
			return
		}
		if err := os.MkdirAll(tempDir, 0o755); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "创建临时目录失败: " + err.Error(), "data": nil})
			return
		}

		tempName := uuid.NewString()
		if info.IsDir() {
			tempName += ".zip"
		} else {
			tempName += filepath.Ext(req.Name)
		}
		tempPath := filepath.Join(tempDir, tempName)

		var materializedSize int64
		if info.IsDir() {
			materializedSize, err = zipFolder(fullPath, tempPath)
			if err != nil {
				logger.Logger.Error("打包目录失败", zap.String("dir", fullPath), zap.Error(err))
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "打包目录失败: " + err.Error(), "data": nil})
				return
			}
		} else {
			materializedSize = info.Size()
			if sameVolume(fullPath, tempPath) {
				err = os.Link(fullPath, tempPath)
				if err != nil {
					logger.Logger.Warn("硬链接失败，回退为复制",
						zap.String("src", fullPath), zap.String("dst", tempPath), zap.Error(err))
					err = copyFile(fullPath, tempPath)
				}
			} else {
				err = copyFile(fullPath, tempPath)
			}
			if err != nil {
				_ = os.Remove(tempPath)
				logger.Logger.Error("生成分享临时文件失败", zap.String("src", fullPath), zap.Error(err))
				c.JSON(http.StatusOK, gin.H{"code": 500, "message": "生成分享临时文件失败", "data": nil})
				return
			}
		}

		_, data, err := finalizeShareLink(db, redisClient, userID, fullPath, req.Name, tempName, "disk",
			materializedSize, req.ExpireMinutes, info.IsDir(), false)
		if err != nil {
			_ = os.Remove(tempPath)
			logger.Logger.Error("写入分享链接记录失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "写入分享链接记录失败", "data": nil})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "ok",
			"data":    data,
		})
	}
}

// sameVolume 判断两个路径是否在同一磁盘卷（Windows 按盘符，Linux 卷名为空时依赖硬链接回退）。
func sameVolume(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

// zipFolder 将目录打包为 zip（Store 不压缩，仅作容器，速度快），返回 zip 文件大小。
// zip 内以目录名为根（解压后直接得到该目录）。
func zipFolder(srcDir, dstZip string) (int64, error) {
	out, err := os.Create(dstZip)
	if err != nil {
		return 0, err
	}
	zw := zip.NewWriter(out)
	base := filepath.Dir(srcDir)

	err = filepath.Walk(srcDir, func(p string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if fi.IsDir() {
			_, err := zw.CreateHeader(&zip.FileHeader{Name: rel + "/", Method: zip.Store})
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		h := &zip.FileHeader{Name: rel, Method: zip.Store}
		h.SetMode(fi.Mode())
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})

	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if oerr := out.Close(); err == nil {
		err = oerr
	}
	if err != nil {
		if rmErr := os.Remove(dstZip); rmErr != nil && !os.IsNotExist(rmErr) {
			logger.Logger.Warn("清理失败的 zip 文件失败", zap.String("path", dstZip), zap.Error(rmErr))
		}
		return 0, err
	}

	fi, err := os.Stat(dstZip)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
