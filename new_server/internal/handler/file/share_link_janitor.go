package file

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// ShareLink.Status 取值：1=有效，0=自然到期，2=创建者主动吊销。
const (
	shareStatusActive  int8 = 1
	shareStatusExpired int8 = 0
	shareStatusRevoked int8 = 2
)

// watchShareLink 每个分享链接一个独立协程：到期后销毁自己管理的临时文件并退出。
func watchShareLink(db *gorm.DB, rdb *redis.Client, shareCode, tempPath string, d time.Duration) {
	if d <= 0 {
		expireShareLink(db, rdb, shareCode, tempPath)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	<-timer.C
	expireShareLink(db, rdb, shareCode, tempPath)
}

// expireShareLink 自然到期销毁（到期协程 / 兜底 janitor 调用）。
func expireShareLink(db *gorm.DB, rdb *redis.Client, shareCode, tempPath string) {
	destroyShareLink(db, rdb, shareCode, tempPath, shareStatusExpired)
}

// destroyShareLink 销毁一个分享链接：删临时文件、标记 DB 状态、清 Redis key。
// 幂等（WHERE status = 1 保证只有仍处于"有效"的记录会被改写一次），可被到期协程、
// 兜底 janitor、吊销接口重复调用而不会互相打架。
func destroyShareLink(db *gorm.DB, rdb *redis.Client, shareCode, tempPath string, finalStatus int8) {
	if tempPath != "" {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			logger.Logger.Warn("删除分享临时文件失败", zap.String("path", tempPath), zap.Error(err))
		}
	}
	now := time.Now()
	if err := db.Model(&model.ShareLink{}).
		Where("share_code = ? AND status = ?", shareCode, shareStatusActive).
		Updates(map[string]interface{}{"status": finalStatus, "expired_at": now}).Error; err != nil {
		logger.Logger.Warn("更新分享链接状态失败", zap.String("share_code", shareCode), zap.Error(err))
	}
	if rdb != nil {
		if err := rdb.Del(context.Background(), shareLinkRedisPrefix+shareCode).Err(); err != nil {
			logger.Logger.Warn("删除分享链接 Redis key 失败", zap.String("share_code", shareCode), zap.Error(err))
		}
	}
	logger.Logger.Info("分享链接已销毁", zap.String("share_code", shareCode), zap.Int8("final_status", finalStatus))
}

// StartShareLinkJanitor 定时清理器：周期扫描已到期的分享链接并销毁。
// 兜底服务重启后内存协程丢失的场景；正常到期的链接由其自身协程负责销毁。
func StartShareLinkJanitor(db *gorm.DB, rdb *redis.Client) {
	go func() {
		interval := config.Conf.Share.CleanIntervalMinutes
		if interval <= 0 {
			interval = 30
		}
		cleanupShareLinksOnce(db, rdb)
		ticker := time.NewTicker(time.Duration(interval) * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanupShareLinksOnce(db, rdb)
		}
	}()
}

func cleanupShareLinksOnce(db *gorm.DB, rdb *redis.Client) {
	var links []model.ShareLink
	if err := db.Where("status = ? AND expire_time <= ?", shareStatusActive, time.Now()).Find(&links).Error; err != nil {
		logger.Logger.Warn("扫描过期分享链接失败", zap.Error(err))
		return
	}
	if len(links) == 0 {
		return
	}
	tempDir := config.Conf.Share.TempPath
	for i := range links {
		tempPath := ""
		if tempDir != "" {
			tempPath = filepath.Join(tempDir, links[i].TempName)
		}
		expireShareLink(db, rdb, links[i].ShareCode, tempPath)
	}
	logger.Logger.Info("定时清理过期分享链接", zap.Int("count", len(links)))
}
