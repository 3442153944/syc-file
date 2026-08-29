package sync

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// DefaultExcludes 是每个同步文件夹强制携带的忽略规则：
// 临时暂存目录、冲突隔离目录、Office 锁文件都不得参与同步，否则会递归同步或抓到中间态。
const DefaultExcludes = ".synctmp/\n.syncpending/\n~$*"

// folderByID 按主键取 folder，不存在返回 false。
func (e *Engine) folderByID(id uint64) (model.SyncFolder, bool) {
	var f model.SyncFolder
	if err := e.db.First(&f, id).Error; err != nil {
		return model.SyncFolder{}, false
	}
	return f, true
}

// UpsertFolder 创建或更新该用户唯一的同步文件夹配置（DB 唯一约束 user_id 保证幂等）。
// 不覆盖 excludes：避免每次保存把用户/系统已生效的排除规则重置成默认值。
func (e *Engine) UpsertFolder(userID uint, ownerDeviceID, name, localPath, remotePath, direction string) (*model.SyncFolder, error) {
	if !config.Conf.IsPathAllowed(remotePath) {
		return nil, fmt.Errorf("remote path not allowed: %s", remotePath)
	}
	if direction == "" {
		direction = model.DirectionTwoWay
	}
	f := &model.SyncFolder{
		UserID:        userID,
		Name:          name,
		LocalPath:     localPath,
		RemotePath:    remotePath,
		Direction:     direction,
		Enabled:       true,
		Excludes:      DefaultExcludes,
		OwnerDeviceID: ownerDeviceID,
	}
	err := e.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "local_path", "remote_path", "direction", "owner_device_id", "enabled",
		}),
	}).Create(f).Error
	if err != nil {
		return nil, err
	}
	var saved model.SyncFolder
	if err := e.db.Where("user_id = ?", userID).First(&saved).Error; err != nil {
		return nil, err
	}

	// 远端目录是权威存储位置，保存后异步收编磁盘上早于本次登记的内容（不阻塞响应）。
	go func(f model.SyncFolder) {
		if n, rerr := e.ReconcileFolderFilesystem(f); rerr != nil {
			logger.Logger.Warn("远端目录收编失败", zap.Uint64("folder_id", f.ID), zap.Error(rerr))
		} else if n > 0 {
			logger.Logger.Info("远端目录收编完成", zap.Uint64("folder_id", f.ID), zap.Int("adopted", n))
		}
	}(saved)

	return &saved, nil
}

// GetFolder 取该用户唯一的同步文件夹配置，不存在返回 nil, nil。
func (e *Engine) GetFolder(userID uint) (*model.SyncFolder, error) {
	var f model.SyncFolder
	err := e.db.Where("user_id = ?", userID).First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// UpdateFolder 更新该用户唯一的同步文件夹配置（如改远端路径则校验白名单）。
func (e *Engine) UpdateFolder(userID uint, updates map[string]interface{}) error {
	if rp, ok := updates["remote_path"].(string); ok && rp != "" {
		if !config.Conf.IsPathAllowed(rp) {
			return fmt.Errorf("remote path not allowed: %s", rp)
		}
	}
	res := e.db.Model(&model.SyncFolder{}).Where("user_id = ?", userID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}

// DeleteFolder 删除该用户唯一的同步文件夹配置。
func (e *Engine) DeleteFolder(userID uint) error {
	res := e.db.Where("user_id = ?", userID).Delete(&model.SyncFolder{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}
