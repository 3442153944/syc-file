package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// resolveShareTempPath 根据分享记录算出内容在磁盘上的真实路径：
// 内存态没有真实文件，返回空字符串；粘贴快传落盘的在 QuickShare.BasePath/<user_id>/ 下；
// 其余（含 storage_kind 为空字符串的历史/边界情况，一律当 disk 处理）在 Share.TempPath 下。
// 供下载处理器和本文件的兜底清理共用，避免两处各写一份路径拼接逻辑。
func resolveShareTempPath(link model.ShareLink) string {
	if link.StorageKind == "memory" {
		return ""
	}
	if link.IsQuickShare {
		return filepath.Join(config.Conf.QuickShare.BasePath, strconv.FormatUint(uint64(link.UserID), 10), link.TempName)
	}
	return filepath.Join(config.Conf.Share.TempPath, link.TempName)
}

// finalizeShareLink 分享创建的公共收尾：写 DB 行、写 Redis meta、启动到期协程、拼装响应体。
// 手动分享已有文件/目录（create_share_file_link.go）和粘贴快传（quick_share.go）共用这一份，
// 保证 Redis meta 里 is_quick_share/storage_kind 两条路径永远一致，不会出现两套拼装、
// 改一处忘了改另一处的情况。调用方负责在返回 err 时自行清理已经写好的临时文件/内存内容
// （finalize 本身不知道调用方是否已经落盘/落内存)。
func finalizeShareLink(
	db *gorm.DB, redisClient *redis.Client,
	userID uint, originalPath, fileName, tempName, storageKind string,
	size int64, expireMinutes int, isDir, isQuickShare bool,
) (shareCode string, data gin.H, err error) {
	shareCode = uuid.NewString()
	expireTime := time.Now().Add(time.Duration(expireMinutes) * time.Minute)
	link := &model.ShareLink{
		UserID:       userID,
		ShareCode:    shareCode,
		OriginalPath: originalPath,
		FileName:     fileName,
		TempName:     tempName,
		FileSize:     size,
		ExpireTime:   expireTime,
		Status:       shareStatusActive,
		IsQuickShare: isQuickShare,
		StorageKind:  storageKind,
	}
	if createErr := db.Create(link).Error; createErr != nil {
		return "", nil, createErr
	}

	meta := map[string]interface{}{
		"share_code":     shareCode,
		"temp_name":      tempName,
		"original_path":  originalPath,
		"file_name":      fileName,
		"is_dir":         isDir,
		"expire_time":    expireTime.Format(time.RFC3339),
		"is_quick_share": isQuickShare,
		"storage_kind":   storageKind,
		"user_id":        userID,
	}
	if metaBytes, merr := json.Marshal(meta); merr == nil {
		if serr := redisClient.Set(context.Background(), shareLinkRedisPrefix+shareCode, metaBytes, time.Until(expireTime)).Err(); serr != nil {
			logger.Logger.Warn("分享链接写入 Redis 失败", zap.String("share_code", shareCode), zap.Error(serr))
		}
	}

	go watchShareLink(db, redisClient, shareCode, resolveShareTempPath(*link), time.Until(expireTime))

	logger.Logger.Info("分享链接创建成功",
		zap.Uint("user_id", userID), zap.String("share_code", shareCode),
		zap.String("temp_name", tempName), zap.Bool("is_dir", isDir),
		zap.Bool("is_quick_share", isQuickShare), zap.String("storage_kind", storageKind))

	return shareCode, gin.H{
		"share_code":  shareCode,
		"temp_name":   tempName,
		"file_name":   fileName,
		"file_size":   size,
		"is_dir":      isDir,
		"expire_time": expireTime.Format(time.RFC3339),
		"url_path":    "/v1/file/share-link/download/" + shareCode,
	}, nil
}

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

// destroyShareLink 销毁一个分享链接：删临时文件（若有）、清内存态内容（若有）、
// 标记 DB 状态、清 Redis key。幂等（WHERE status = 1 保证只有仍处于"有效"的记录会被改写一次），
// 可被到期协程、兜底 janitor、吊销接口重复调用而不会互相打架。
func destroyShareLink(db *gorm.DB, rdb *redis.Client, shareCode, tempPath string, finalStatus int8) {
	if tempPath != "" {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			logger.Logger.Warn("删除分享临时文件失败", zap.String("path", tempPath), zap.Error(err))
		}
	}
	// 磁盘态该 key 本来就不存在，Delete 是无害空操作
	quickShareMemStore.Delete(shareCode)
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
// 启动时先额外做一次内存态分享的即时清理（见 expireStaleMemoryShares），
// 再进入周期扫描。
func StartShareLinkJanitor(db *gorm.DB, rdb *redis.Client) {
	go func() {
		expireStaleMemoryShares(db, rdb)
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

// expireStaleMemoryShares 进程重启后，内存态分享的字节内容已经随 quickShareMemStore
// 一起清空，但 DB 行还是 status=1 且 expire_time 还没到——不主动处理的话，这些记录会
// 一直算进配额 SUM 直到自然到期，造成"内容早没了但配额还占着"的假象。启动时先把它们
// 全部按正常销毁流程处理一遍（内容反正已经不可恢复，没必要等到自然到期）。
func expireStaleMemoryShares(db *gorm.DB, rdb *redis.Client) {
	var links []model.ShareLink
	if err := db.Where("status = ? AND storage_kind = ?", shareStatusActive, "memory").Find(&links).Error; err != nil {
		logger.Logger.Warn("扫描重启残留的内存态分享失败", zap.Error(err))
		return
	}
	if len(links) == 0 {
		return
	}
	for i := range links {
		expireShareLink(db, rdb, links[i].ShareCode, "")
	}
	logger.Logger.Info("已清理重启前残留的内存态分享", zap.Int("count", len(links)))
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
	for i := range links {
		expireShareLink(db, rdb, links[i].ShareCode, resolveShareTempPath(links[i]))
	}
	logger.Logger.Info("定时清理过期分享链接", zap.Int("count", len(links)))
}
