package sync

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"syc-file/config"
	"syc-file/internal/model"
)

const SourceServer = "server"

func (e *Engine) CompleteTask(taskID uint64, hash string) {
	var task model.SyncTask
	if err := e.db.First(&task, taskID).Error; err != nil {
		return
	}
	if task.SyncStatus != model.SyncStatusSyncing {
		return
	}
	now := nowPtr()
	updates := map[string]interface{}{
		"sync_status":  model.SyncStatusCompleted,
		"progress":     100,
		"completed_at": now,
	}
	if hash != "" {
		updates["file_hash"] = hash
	}
	e.db.Model(&task).Updates(updates)
	ctx := context.Background()
	e.store.DecPending(ctx, task.UserID)
	e.store.ReleaseFileLock(ctx, task.RelativePath)
	e.store.ResetProgress(ctx, taskID)
}

func (e *Engine) FailTask(taskID uint64, errMsg string) {
	var task model.SyncTask
	if err := e.db.First(&task, taskID).Error; err != nil {
		return
	}
	if task.SyncStatus != model.SyncStatusSyncing {
		return
	}
	ctx := context.Background()
	e.store.ReleaseFileLock(ctx, task.RelativePath)
	rc := task.RetryCount + 1
	if rc > task.MaxRetry {
		e.db.Model(&task).Updates(map[string]interface{}{
			"sync_status":   model.SyncStatusFailed,
			"error_message": errMsg,
			"completed_at":  nowPtr(),
			"retry_count":   rc,
		})
		e.store.DecPending(ctx, task.UserID)
		return
	}
	e.db.Model(&task).Updates(map[string]interface{}{
		"sync_status":   model.SyncStatusPending,
		"error_message": errMsg,
		"retry_count":   rc,
		"started_at":    nil,
		"progress":      0,
	})
	e.store.EnqueueTask(ctx, taskID)
}

func (e *Engine) UpdateTaskProgress(taskID uint64, progress int, bytes int64) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	e.db.Model(&model.SyncTask{}).Where("id = ?", taskID).Update("progress", progress)
	e.store.UpdateProgress(context.Background(), taskID, progress, bytes)
}

func (e *Engine) HandleScan(userID uint, deviceID string, report ScanReport) error {
	var folder model.SyncFolder
	if err := e.db.First(&folder, report.FolderID).Error; err != nil {
		return err
	}
	if folder.UserID != userID {
		return fmt.Errorf("sync folder unavailable")
	}
	if folder.Direction == model.DirectionUploadOnly {
		return nil
	}

	remotePrefix := filepath.Clean(folder.RemotePath)
	var files []model.File
	like := strings.ReplaceAll(filepath.ToSlash(remotePrefix), "/", "\\") + string(filepath.Separator) + "%"
	e.db.Where("user_id = ? AND file_path LIKE ?", userID, like).Find(&files)

	relToFile := make(map[string]model.File)
	for _, f := range files {
		rel := relFromPath(remotePrefix, f.FilePath)
		if rel == "" {
			continue
		}
		relToFile[rel] = f
	}

	have := make(map[string]bool)
	items := report.Items
	for _, it := range items {
		have[it.RelativePath] = true
	}

	for rel, f := range relToFile {
		if f.IsDeleted {
			continue
		}
		it, ok := findItem(items, rel)
		if !ok {
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, reportFromFolder(folder, f, rel), f.ID, taskTypeForFile(f), hashOf(f))
		} else if !f.IsDirectory && hashOf(f) != "" && hashOf(f) != it.FileHash {
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, reportFromFolder(folder, f, rel), f.ID, model.TaskTypeDownload, hashOf(f))
		}
	}

	for _, it := range items {
		f, ok := relToFile[it.RelativePath]
		if !ok || f.IsDeleted {
			r := FileChangeReport{
				FolderID:     folder.ID,
				RelativePath: it.RelativePath,
				FileName:     it.FileName,
				FileSize:     it.FileSize,
				FileHash:     "",
				IsDir:        it.IsDir,
				Action:       model.FileChangeDelete,
			}
			fileID := uint64(0)
			if ok {
				fileID = f.ID
			}
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, r, fileID, model.TaskTypeDelete, "")
		}
	}
	return nil
}

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
		OwnerDeviceID: ownerDeviceID,
	}
	if err := e.db.Create(f).Error; err != nil {
		return nil, err
	}
	return f, nil
}

func (e *Engine) ListFolders(userID uint) ([]model.SyncFolder, error) {
	var fs []model.SyncFolder
	if err := e.db.Where("user_id = ?", userID).Order("id desc").Find(&fs).Error; err != nil {
		return nil, err
	}
	return fs, nil
}

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

func (e *Engine) ListTasks(userID uint, status, deviceID string, limit int) ([]model.SyncTask, error) {
	q := e.db.Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("sync_status = ?", status)
	}
	if deviceID != "" {
		q = q.Where("target_device_id = ? OR source_device_id = ?", deviceID, deviceID)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var ts []model.SyncTask
	if err := q.Order("id desc").Limit(limit).Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

func (e *Engine) PendingTasksForDevice(userID uint, deviceID string) ([]model.SyncTask, error) {
	var ts []model.SyncTask
	if err := e.db.Where("user_id = ? AND target_device_id = ? AND sync_status = ?", userID, deviceID, model.SyncStatusPending).Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

func (e *Engine) ListConflicts(userID uint) ([]model.SyncTask, error) {
	var ts []model.SyncTask
	if err := e.db.Where("user_id = ? AND conflict = ?", userID, true).Order("id desc").Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

func (e *Engine) ResolveConflict(userID uint, taskID uint64) error {
	res := e.db.Where("id = ? AND user_id = ? AND conflict = ?", taskID, userID, true).Delete(&model.SyncTask{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("conflict record not found")
	}
	return nil
}

func findItem(items []ScanItem, rel string) (ScanItem, bool) {
	for _, it := range items {
		if it.RelativePath == rel {
			return it, true
		}
	}
	return ScanItem{}, false
}

func reportFromFolder(folder model.SyncFolder, f model.File, rel string) FileChangeReport {
	return FileChangeReport{
		FolderID:     folder.ID,
		RelativePath: rel,
		FileName:     f.FileName,
		FileSize:     sizeOf(f),
		FileHash:     hashOf(f),
		IsDir:        f.IsDirectory,
	}
}

func hashOf(f model.File) string {
	if f.FileHash != nil {
		return *f.FileHash
	}
	return ""
}

func sizeOf(f model.File) int64 {
	if f.FileSize != nil {
		return *f.FileSize
	}
	return 0
}

func taskTypeForFile(f model.File) string {
	if f.IsDirectory {
		return model.TaskTypeMkdir
	}
	return model.TaskTypeDownload
}

func relFromPath(prefix, fullPath string) string {
	prefixSlash := filepath.ToSlash(filepath.Clean(prefix))
	fullSlash := filepath.ToSlash(filepath.Clean(fullPath))
	rel := strings.TrimPrefix(fullSlash, prefixSlash)
	rel = strings.TrimPrefix(rel, "/")
	return rel
}

func nowPtr() time.Time {
	return time.Now()
}
