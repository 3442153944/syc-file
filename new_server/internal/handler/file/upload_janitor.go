package file

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/pkg/logger"
)

// StartTempJanitor 启动后台清理器，周期性删除各盘临时目录里超过 TTL 的僵尸 .part 文件
// （会话过期/进程崩溃遗留、Redis 已无对应会话）。活跃上传每写一片会刷新 mtime，故按 mtime 判定安全。
func StartTempJanitor() {
	go func() {
		cleanupTempOnce()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanupTempOnce()
		}
	}()
}

func cleanupTempOnce() {
	threshold := time.Now().Add(-uploadSessionTTL)
	for _, drive := range config.Conf.File.AllowedPaths {
		vol := filepath.VolumeName(drive + string(filepath.Separator))
		if vol == "" {
			vol = strings.TrimSuffix(drive, string(filepath.Separator))
		}
		dir := filepath.Join(vol+string(filepath.Separator), config.Conf.File.Storage.BasePath, config.Conf.File.Storage.TempPath)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // 目录不存在很正常
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".part") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(threshold) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if err := os.Remove(p); err == nil {
				logger.Logger.Info("清理僵尸临时文件", zap.String("path", p))
			}
		}
	}
}
