package file

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/logger"
	"syc-file/pkg/synccore"
	"syc-file/pkg/volroot"
)

// StartTempJanitor 启动后台清理器，周期性删除各盘临时目录里超过 TTL 的僵尸 .part 文件
// （会话过期/进程崩溃遗留、Redis 已无对应会话）。活跃上传每写一片会刷新 mtime，故按 mtime 判定安全。
func StartTempJanitor(db *gorm.DB) {
	go func() {
		cleanupTempOnce(db)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanupTempOnce(db)
		}
	}()
}

func cleanupTempOnce(databases ...*gorm.DB) {
	threshold := time.Now().Add(-uploadSessionTTL)
	dirs := make([]string, 0)
	for _, drive := range config.Conf.File.AllowedPaths {
		vol := volroot.RootOf(drive)
		base := config.Conf.File.Storage.BasePath
		if !filepath.IsAbs(base) {
			base = filepath.Join(vol, base)
		}
		dirs = append(dirs, filepath.Join(base, config.Conf.File.Storage.TempPath))
	}
	if len(databases) > 0 && databases[0] != nil {
		db := databases[0]
		var spaces []model.UploadSpace
		if db.Find(&spaces).Error == nil {
			for _, space := range spaces {
				var pool model.StoragePool
				if db.First(&pool, space.PoolID).Error == nil {
					root := storage.Root(pool, space)
					dir := filepath.Join(root, ".uploads")
					if storage.Within(root, dir) {
						dirs = append(dirs, dir)
					}
				}
			}
		}
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // 目录不存在很正常
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".part") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(threshold) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			_ = synccore.Evict(p) // 先关 Rust 侧缓存句柄再删文件
			if err := os.Remove(p); err == nil {
				logger.Logger.Info("清理僵尸临时文件", zap.String("path", p))
			}
		}
	}
}
