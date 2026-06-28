package sync

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
)

// isConflict 基于 base CAS 判定一次新建/修改上报是否与 trunk 冲突。
//
// 不算冲突的情形：目录、trunk 记录已删除、trunk 尚无内容、内容未变（hash 相同）。
// 算冲突：trunk 有内容且与新内容不同，且客户端 base_hash != trunk 当前 hash
// （即源端基于一个过期版本做的修改 —— 并发分叉）。
func (e *Engine) isConflict(file model.File, r FileChangeReport) bool {
	if r.IsDir || file.IsDirectory || file.IsDeleted {
		return false
	}
	serverHash := derefStr(file.FileHash)
	if serverHash == "" || serverHash == r.FileHash {
		return false
	}
	return r.BaseHash != serverHash
}

// handleConflict 记录冲突待办并通知源设备隔离其本地副本。不触碰 trunk。
func (e *Engine) handleConflict(userID uint, source string, folder model.SyncFolder, r FileChangeReport, file model.File) {
	conflictID := e.recordConflict(userID, source, folder, r, file)
	e.notifyConflict(source, folder, r, file, conflictID)
}

// recordConflict 把冲突写入 sync_conflict 待办表（pending），返回记录 ID。
func (e *Engine) recordConflict(userID uint, source string, folder model.SyncFolder, r FileChangeReport, file model.File) uint64 {
	c := &model.SyncConflict{
		UserID:        userID,
		DeviceID:      source,
		FolderID:      folder.ID,
		FileID:        file.ID,
		RelativePath:  r.RelativePath,
		FileName:      r.FileName,
		ServerHash:    file.FileHash,
		LocalHash:     ptrStr(r.FileHash),
		BaseHash:      ptrStr(r.BaseHash),
		ServerVersion: file.Version,
		Status:        model.ConflictStatusPending,
	}
	if err := e.db.Create(c).Error; err != nil {
		logger.Logger.Error("记录同步冲突失败", zap.Error(err))
		return 0
	}
	return c.ID
}

// notifyConflict 通知源设备：把本地分叉拷入 .syncpending，主目录收敛为 server_hash。
func (e *Engine) notifyConflict(deviceID string, folder model.SyncFolder, r FileChangeReport, file model.File, conflictID uint64) {
	_ = ws.SendToDevice(deviceID, ws.MessageTypeFileSync, map[string]interface{}{
		"event":          ws.SyncEventConflict,
		"conflict_id":    conflictID,
		"folder_id":      folder.ID,
		"relative_path":  r.RelativePath,
		"file_name":      r.FileName,
		"server_hash":    derefStr(file.FileHash),
		"server_version": file.Version,
		"base_hash":      r.BaseHash,
		"local_hash":     r.FileHash,
	})
}

// ListConflicts 返回该用户待处理（pending）的冲突待办。
func (e *Engine) ListConflicts(userID uint) ([]model.SyncConflict, error) {
	var cs []model.SyncConflict
	if err := e.db.Where("user_id = ? AND status = ?", userID, model.ConflictStatusPending).
		Order("id desc").Find(&cs).Error; err != nil {
		return nil, err
	}
	return cs, nil
}

// ResolveConflict 处理冲突待办：
//   - accept_server：标记已解决，补派一个 download 任务确保源设备主目录收敛为 trunk；
//   - keep_local   ：标记已解决，回执当前 trunk hash，客户端据此以 trunk 为 base 重新上传本地副本。
func (e *Engine) ResolveConflict(userID uint, conflictID uint64, resolution string) error {
	if resolution != model.ResolutionAcceptServer && resolution != model.ResolutionKeepLocal {
		return fmt.Errorf("无效的解决方式")
	}
	var c model.SyncConflict
	if err := e.db.Where("id = ? AND user_id = ?", conflictID, userID).First(&c).Error; err != nil {
		return fmt.Errorf("冲突记录不存在")
	}
	if c.Status == model.ConflictStatusResolved {
		return nil
	}
	now := time.Now()
	e.db.Model(&c).Updates(map[string]interface{}{
		"status":      model.ConflictStatusResolved,
		"resolution":  resolution,
		"resolved_at": now,
	})

	// 取 trunk 当前版本作为回执 / 重提交 base
	serverHash := ""
	var file model.File
	haveFile := e.db.First(&file, c.FileID).Error == nil
	if haveFile {
		serverHash = derefStr(file.FileHash)
	}

	if resolution == model.ResolutionAcceptServer && haveFile && !file.IsDeleted {
		if folder, ok := e.folderByID(c.FolderID); ok {
			r := reportFromFolder(folder, file, c.RelativePath)
			e.createAndEnqueueTask(userID, SourceServer, c.DeviceID, folder, r, file.ID, taskTypeForFile(file), serverHash)
		}
	}

	e.notifyConflictResolved(c.DeviceID, conflictID, resolution, serverHash)
	return nil
}

// DeleteConflict 删除一条冲突记录（清理残留）。
func (e *Engine) DeleteConflict(userID uint, id uint64) error {
	res := e.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.SyncConflict{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("conflict record not found")
	}
	return nil
}

// notifyConflictResolved 把待办处理结果回执给设备。
func (e *Engine) notifyConflictResolved(deviceID string, conflictID uint64, resolution, serverHash string) {
	_ = ws.SendToDevice(deviceID, ws.MessageTypeFileSync, map[string]interface{}{
		"event":       ws.SyncEventConflictResolved,
		"conflict_id": conflictID,
		"resolution":  resolution,
		"server_hash": serverHash,
	})
}
