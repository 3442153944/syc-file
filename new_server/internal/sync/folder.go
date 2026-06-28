package sync

import (
	"fmt"

	"syc-file/config"
	"syc-file/internal/model"
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

// CreateFolder 新建同步文件夹配置（校验远端路径白名单）。
func (e *Engine) CreateFolder(userID uint, ownerDeviceID, name, localPath, remotePath, direction string) (*model.SyncFolder, error) {
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
	if err := e.db.Create(f).Error; err != nil {
		return nil, err
	}
	return f, nil
}

// ListFolders 列出该用户的全部同步文件夹。
func (e *Engine) ListFolders(userID uint) ([]model.SyncFolder, error) {
	var fs []model.SyncFolder
	if err := e.db.Where("user_id = ?", userID).Order("id desc").Find(&fs).Error; err != nil {
		return nil, err
	}
	return fs, nil
}

// UpdateFolder 更新同步文件夹（如改远端路径则校验白名单）。
func (e *Engine) UpdateFolder(userID uint, id uint64, updates map[string]interface{}) error {
	if rp, ok := updates["remote_path"].(string); ok && rp != "" {
		if !config.Conf.IsPathAllowed(rp) {
			return fmt.Errorf("remote path not allowed: %s", rp)
		}
	}
	res := e.db.Model(&model.SyncFolder{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}

// DeleteFolder 删除同步文件夹配置。
func (e *Engine) DeleteFolder(userID uint, id uint64) error {
	res := e.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.SyncFolder{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}
