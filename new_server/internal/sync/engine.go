package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
	"syc-file/pkg/sync_store"
)

type Engine struct {
	db    *gorm.DB
	rdb   *redis.Client
	store *sync_store.SyncStore
	cfg   config.SyncConfig
	hub   *ws.Hub
}

var Global *Engine

func InitSync(db *gorm.DB, rdb *redis.Client, cfg config.SyncConfig) *Engine {
	conc := cfg.WorkerConcurrency
	if conc <= 0 {
		conc = 4
	}
	e := &Engine{
		db:    db,
		rdb:   rdb,
		store: sync_store.New(rdb),
		cfg:   cfg,
		hub:   ws.GetHub(),
	}
	ws.SetFileSyncHandler(e.handleWSMessage)

	w := &Worker{engine: e}
	for i := 0; i < conc; i++ {
		go w.Run(context.Background())
	}
	go w.Reaper(context.Background())
	Global = e
	logger.Logger.Info("同步引擎初始化完成", zap.Int("workers", conc))
	return e
}

func (e *Engine) handleWSMessage(conn *ws.Connection, msg *ws.Message, event string, content map[string]interface{}) {
	userID := conn.UserID
	deviceID := conn.Device.DeviceID
	switch event {
	case ws.SyncEventFileChanged:
		var r FileChangeReport
		if bindContent(content, &r) {
			if err := e.HandleFileChange(userID, deviceID, r); err != nil {
				logger.Logger.Warn("处理文件变更上报失败", zap.Uint("user_id", userID), zap.Error(err))
			}
		}
	case ws.SyncEventTaskCompleted:
		var p struct {
			TaskID   uint64 `json:"task_id"`
			FileHash string `json:"file_hash"`
		}
		if bindContent(content, &p) {
			e.CompleteTask(p.TaskID, p.FileHash)
		}
	case ws.SyncEventTaskFailed:
		var p struct {
			TaskID uint64 `json:"task_id"`
			Error  string `json:"error"`
		}
		if bindContent(content, &p) {
			e.FailTask(p.TaskID, p.Error)
		}
	case ws.SyncEventTaskProgress:
		var p struct {
			TaskID   uint64 `json:"task_id"`
			Progress int    `json:"progress"`
			Bytes    int64  `json:"bytes"`
		}
		if bindContent(content, &p) {
			e.UpdateTaskProgress(p.TaskID, p.Progress, p.Bytes)
		}
	case ws.SyncEventScanResult:
		var r ScanReport
		if bindContent(content, &r) {
			e.HandleScan(userID, deviceID, r)
		}
	}
}

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

func (e *Engine) handleCreateOrModify(userID uint, source string, folder model.SyncFolder, r FileChangeReport, remotePath string) error {
	var file model.File
	err := e.db.Where("user_id = ? AND file_path = ?", userID, remotePath).First(&file).Error

	if r.IsDir {
		if mkErr := os.MkdirAll(remotePath, 0755); mkErr != nil {
			logger.Logger.Warn("同步创建目录失败", zap.String("path", remotePath), zap.Error(mkErr))
		}
	}

	if err == nil {
		if !file.IsDeleted && file.FileHash != nil && *file.FileHash != "" && *file.FileHash != r.FileHash {
			e.notifyConflict(source, folder, r, *file.FileHash)
			e.recordConflict(userID, source, folder, r)
			return nil
		}
		updates := map[string]interface{}{
			"file_size":  r.FileSize,
			"version":    file.Version + 1,
			"is_deleted": false,
			"deleted_at": nil,
		}
		if r.FileHash != "" {
			updates["file_hash"] = r.FileHash
		}
		e.db.Model(&file).Updates(updates)
		e.dispatchToOthers(userID, source, folder, r, file.ID, taskTypeFor(r), r.FileHash)
		return nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	hash := ptrStr(r.FileHash)
	size := r.FileSize
	newFile := model.File{
		UserID:      userID,
		FileName:    r.FileName,
		FilePath:    remotePath,
		FileSize:    &size,
		FileHash:    hash,
		IsDirectory: r.IsDir,
		Version:     1,
	}
	if err := e.db.Create(&newFile).Error; err != nil {
		return err
	}
	e.dispatchToOthers(userID, source, folder, r, newFile.ID, taskTypeFor(r), r.FileHash)
	return nil
}

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

func (e *Engine) notifyConflict(deviceID string, folder model.SyncFolder, r FileChangeReport, serverHash string) {
	_ = ws.SendToDevice(deviceID, ws.MessageTypeFileSync, map[string]interface{}{
		"event":         ws.SyncEventConflict,
		"folder_id":     folder.ID,
		"relative_path": r.RelativePath,
		"file_name":     r.FileName,
		"server_hash":   serverHash,
		"local_hash":    r.FileHash,
	})
}

func (e *Engine) recordConflict(userID uint, source string, folder model.SyncFolder, r FileChangeReport) {
	task := &model.SyncTask{
		UserID:         userID,
		SourceDeviceID: source,
		TargetDeviceID: source,
		FolderID:       folder.ID,
		TaskType:       model.TaskTypeDelete,
		SyncStatus:     model.SyncStatusConflict,
		Conflict:       true,
		Direction:      "conflict",
		RelativePath:   r.RelativePath,
		FileName:       r.FileName,
		FileSize:       r.FileSize,
		MaxRetry:       e.maxRetry(),
	}
	if err := e.db.Create(task).Error; err != nil {
		logger.Logger.Error("记录冲突任务失败", zap.Error(err))
	}
}

func (e *Engine) pushTaskCreated(task *model.SyncTask, folder model.SyncFolder) {
	remotePath := filepath.Join(filepath.FromSlash(folder.RemotePath), filepath.FromSlash(task.RelativePath))
	notify := TaskNotify{
		Event:        ws.SyncEventTaskCreated,
		TaskID:       task.ID,
		TaskType:     task.TaskType,
		Direction:    task.Direction,
		FolderID:     task.FolderID,
		RelativePath: task.RelativePath,
		FileName:     task.FileName,
		FileSize:     task.FileSize,
		RemotePath:   remotePath,
		RemoteDir:    filepath.Dir(remotePath),
	}
	if task.FileHash != nil {
		notify.FileHash = *task.FileHash
	}
	_ = ws.SendToDevice(task.TargetDeviceID, ws.MessageTypeFileSync, notify)
}

func (e *Engine) maxRetry() int {
	if e.cfg.MaxRetry > 0 {
		return e.cfg.MaxRetry
	}
	return 3
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

func joinRemotePath(folderRemote, rel string) string {
	return filepath.Join(filepath.FromSlash(folderRemote), filepath.FromSlash(rel))
}

func cleanRelPath(rel string) string {
	rel = filepath.ToSlash(rel)
	rel = filepath.Clean(rel)
	rel = strings.TrimPrefix(rel, "/")
	parts := strings.Split(rel, "/")
	var safe []string
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		safe = append(safe, p)
	}
	return strings.Join(safe, "/")
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func bindContent(content map[string]interface{}, v interface{}) bool {
	data, err := json.Marshal(content)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}
