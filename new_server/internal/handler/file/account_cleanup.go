package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/storage"
)

// CleanupAccountShares 清除删除账号的分享副本、快传内容及缓存；失败由账号清理器重试。
func CleanupAccountShares(db *gorm.DB, rdb *redis.Client, uid uint) error {
	var links []model.ShareLink
	if err := db.Find(&links).Error; err != nil {
		return err
	}
	root := ""
	s, p, err := storage.Load(db, uid)
	if err == nil {
		root = storage.Root(p, s)
	} else if !errors.Is(err, storage.ErrUnassigned) {
		return err
	}
	for _, link := range links {
		// 系统管理员替该空间创建的分享也必须撤销，不能留下独立的硬链接副本。
		if link.UserID != uid && (link.IsQuickShare || root == "" || !storage.Within(root, link.OriginalPath)) {
			continue
		}
		quickShareMemStore.Delete(link.ShareCode)
		if link.StorageKind != "memory" {
			// 临时文件名只允许单个文件名，不能信任历史记录中的任意路径。
			if link.TempName == "" || filepath.Base(link.TempName) != link.TempName || strings.ContainsAny(link.TempName, "/\\:") || link.TempName == "." || link.TempName == ".." {
				return storage.ErrDenied
			}
			path, err := filepath.Abs(resolveShareTempPath(link))
			if err != nil {
				return err
			}
			if !storage.Within(filepath.Dir(path), path) {
				return storage.ErrDenied
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if rdb != nil {
			if err := rdb.Del(context.Background(), shareLinkRedisPrefix+link.ShareCode).Err(); err != nil {
				return err
			}
		}
		if err := db.Model(&link).Updates(map[string]any{"status": shareStatusRevoked, "expired_at": time.Now()}).Error; err != nil {
			return err
		}
	}
	// 快传目录还可能留有未完成的文件，目录固定由账号编号推导。
	base, err := filepath.Abs(config.Conf.QuickShare.BasePath)
	if err != nil || config.Conf.QuickShare.BasePath == "" {
		return err
	}
	dir := filepath.Join(base, fmt.Sprint(uid))
	if !storage.Within(base, dir) {
		return storage.ErrDenied
	}
	return os.RemoveAll(dir)
}
