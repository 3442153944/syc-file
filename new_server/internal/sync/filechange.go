package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// HandleFileChange 处理客户端上报的单文件变更：校验 folder/路径，再分流到删除或新建/修改。
func (e *Engine) HandleFileChange(userID uint, sourceDeviceID string, r FileChangeReport) error {
	r.RelativePath = cleanRelPath(r.RelativePath)
	if r.RelativePath == "" {
		return fmt.Errorf("invalid relative_path")
	}
	if r.FileName == "" {
		r.FileName = filepath.Base(filepath.ToSlash(r.RelativePath))
	}
	var folder model.SyncFolder
	if err := e.db.First(&folder, r.FolderID).Error; err != nil {
		return err
	}
	if folder.UserID != userID || !folder.Enabled {
		return fmt.Errorf("sync folder unavailable")
	}
	if folder.Direction == model.DirectionDownloadOnly {
		return fmt.Errorf("folder is download_only, ignore upload report")
	}
	remotePath := joinRemotePath(folder.RemotePath, r.RelativePath)
	if !config.Conf.IsPathAllowed(remotePath) {
		return fmt.Errorf("path not allowed: %s", remotePath)
	}

	if r.Action == model.FileChangeDelete {
		return e.handleDelete(userID, sourceDeviceID, folder, r, remotePath)
	}
	return e.handleCreateOrModify(userID, sourceDeviceID, folder, r, remotePath)
}

// handleDelete 软删 trunk 记录、删除服务端物理文件，并向其它在线设备派发 delete 任务。
func (e *Engine) handleDelete(userID uint, source string, folder model.SyncFolder, r FileChangeReport, remotePath string) error {
	var file model.File
	if err := e.db.Where("user_id = ? AND file_path = ?", userID, remotePath).First(&file).Error; err == nil {
		now := time.Now()
		e.db.Model(&file).Updates(map[string]interface{}{
			"is_deleted": true,
			"deleted_at": now,
			"version":    file.Version + 1,
		})
	}
	if !r.IsDir {
		if err := os.Remove(remotePath); err != nil && !os.IsNotExist(err) {
			logger.Logger.Warn("同步删除服务端文件失败", zap.String("path", remotePath), zap.Error(err))
		}
	}
	e.dispatchToOthers(userID, source, folder, r, file.ID, model.TaskTypeDelete, "")
	return nil
}

// handleCreateOrModify 处理新建/修改上报：
//   - 已存在记录 → base CAS：base 与 trunk 当前 hash 一致才快进，否则记冲突；
//   - 不存在 → 直接建 trunk。
//
// 接受后更新 trunk、追加版本历史，并向其它在线设备派发 download/mkdir 任务。
func (e *Engine) handleCreateOrModify(userID uint, source string, folder model.SyncFolder, r FileChangeReport, remotePath string) error {
	var file model.File
	err := e.db.Where("user_id = ? AND file_path = ?", userID, remotePath).First(&file).Error

	if r.IsDir {
		if mkErr := os.MkdirAll(remotePath, 0755); mkErr != nil {
			logger.Logger.Warn("同步创建目录失败", zap.String("path", remotePath), zap.Error(mkErr))
		}
	}

	if err == nil {
		// trunk 已有该路径：先判冲突（base CAS）
		if e.isConflict(file, r) {
			e.handleConflict(userID, source, folder, r, file)
			return nil
		}
		newVersion := file.Version + 1
		updates := map[string]interface{}{
			"file_size":  r.FileSize,
			"version":    newVersion,
			"is_deleted": false,
			"deleted_at": nil,
		}
		if r.FileHash != "" {
			updates["file_hash"] = r.FileHash
		}
		e.db.Model(&file).Updates(updates)
		e.appendVersion(file.ID, int(newVersion), r.FileSize, r.FileHash, userID)
		e.dispatchToOthers(userID, source, folder, r, file.ID, taskTypeFor(r), r.FileHash)
		return nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// trunk 无该路径：作为新文件建立
	size := r.FileSize
	newFile := model.File{
		UserID:      userID,
		FileName:    r.FileName,
		FilePath:    remotePath,
		FileSize:    &size,
		FileHash:    ptrStr(r.FileHash),
		IsDirectory: r.IsDir,
		Version:     1,
	}
	if err := e.db.Create(&newFile).Error; err != nil {
		return err
	}
	e.appendVersion(newFile.ID, 1, r.FileSize, r.FileHash, userID)
	e.dispatchToOthers(userID, source, folder, r, newFile.ID, taskTypeFor(r), r.FileHash)
	return nil
}

// dispatchToOthers 给该用户除源设备外的所有在线设备各生成一条 pending 任务。
func (e *Engine) dispatchToOthers(userID uint, source string, folder model.SyncFolder, r FileChangeReport, fileID uint64, taskType, hash string) {
	conns := e.hub.GetUserConnections(userID)
	for _, conn := range conns {
		dev := conn.Device.DeviceID
		if dev == "" || dev == source {
			continue
		}
		e.createAndEnqueueTask(userID, source, dev, folder, r, fileID, taskType, hash)
	}
}

// createAndEnqueueTask 落库一条同步任务并入 Redis 队列，返回任务 ID。
func (e *Engine) createAndEnqueueTask(userID uint, source, target string, folder model.SyncFolder, r FileChangeReport, fileID uint64, taskType, hash string) uint64 {
	task := &model.SyncTask{
		UserID:         userID,
		SourceDeviceID: source,
		TargetDeviceID: target,
		FolderID:       folder.ID,
		FileID:         fileID,
		TaskType:       taskType,
		SyncStatus:     model.SyncStatusPending,
		Direction:      directionFor(taskType),
		RelativePath:   r.RelativePath,
		FileName:       r.FileName,
		FileSize:       r.FileSize,
		MaxRetry:       e.maxRetry(),
	}
	if hash != "" {
		h := hash
		task.FileHash = &h
		task.SourceHash = &h
	}
	if err := e.db.Create(task).Error; err != nil || task.ID == 0 {
		logger.Logger.Error("创建同步任务失败", zap.Error(err))
		return 0
	}
	ctx := context.Background()
	e.store.IncPending(ctx, userID)
	e.store.EnqueueTask(ctx, task.ID)
	return task.ID
}

func taskTypeFor(r FileChangeReport) string {
	if r.IsDir {
		return model.TaskTypeMkdir
	}
	return model.TaskTypeDownload
}

func directionFor(taskType string) string {
	switch taskType {
	case model.TaskTypeDelete:
		return "delete"
	case model.TaskTypeMkdir:
		return "mkdir"
	default:
		return model.TaskTypeDownload
	}
}
