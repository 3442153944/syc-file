package sync

import (
	"context"
	"time"

	"go.uber.org/zap"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

type Worker struct {
	engine *Engine
}

func (w *Worker) Run(ctx context.Context) {
	timeout := w.queueTimeout()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		taskID, err := w.engine.store.DequeueTask(ctx, timeout)
		if err != nil {
			logger.Logger.Error("同步任务出队失败", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}
		if taskID == 0 {
			continue
		}
		w.processTask(taskID)
	}
}

func (w *Worker) processTask(taskID uint64) {
	var task model.SyncTask
	if err := w.engine.db.First(&task, taskID).Error; err != nil {
		logger.Logger.Error("同步任务不存在", zap.Uint64("task_id", taskID), zap.Error(err))
		return
	}
	if task.SyncStatus != model.SyncStatusPending {
		return
	}
	if !w.engine.hub.IsDeviceOnline(task.TargetDeviceID) {
		return
	}

	ctx := context.Background()
	ttl := w.lockTTL()
	ok, err := w.engine.store.AcquireFileLock(ctx, task.RelativePath, ttl)
	if err != nil {
		logger.Logger.Error("获取文件锁失败", zap.Uint64("task_id", taskID), zap.Error(err))
		return
	}
	if !ok {
		w.engine.store.EnqueueTask(ctx, taskID)
		return
	}

	now := time.Now()
	if err := w.engine.db.Model(&task).Updates(map[string]interface{}{
		"sync_status": model.SyncStatusSyncing,
		"started_at":  now,
		"progress":    0,
	}).Error; err != nil {
		w.engine.store.ReleaseFileLock(ctx, task.RelativePath)
		return
	}

	var folder model.SyncFolder
	if err := w.engine.db.First(&folder, task.FolderID).Error; err != nil {
		logger.Logger.Warn("同步任务关联文件夹缺失", zap.Uint64("folder_id", task.FolderID))
	} else {
		w.engine.pushTaskCreated(&task, folder)
	}
}

func (w *Worker) Reaper(ctx context.Context) {
	timeout := w.taskTimeout()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reap(ctx, timeout)
		}
	}
}

func (w *Worker) reap(ctx context.Context, timeout time.Duration) {
	now := time.Now()
	cutoff := now.Add(-timeout)

	var stuck []model.SyncTask
	if err := w.engine.db.Where("sync_status = ? AND started_at < ?", model.SyncStatusSyncing, cutoff).Limit(500).Find(&stuck).Error; err == nil {
		for _, task := range stuck {
			rc := task.RetryCount + 1
			if rc > task.MaxRetry {
				errMsg := "task timeout"
				w.engine.db.Model(&task).Updates(map[string]interface{}{
					"sync_status":   model.SyncStatusFailed,
					"error_message": errMsg,
					"completed_at":  now,
				})
				w.engine.store.DecPending(ctx, task.UserID)
				w.engine.store.ReleaseFileLock(ctx, task.RelativePath)
				continue
			}
			w.engine.db.Model(&task).Updates(map[string]interface{}{
				"sync_status": model.SyncStatusPending,
				"retry_count": rc,
				"started_at":  nil,
			})
			w.engine.store.EnqueueTask(ctx, task.ID)
		}
	}

	var pending []model.SyncTask
	if err := w.engine.db.Where("sync_status = ?", model.SyncStatusPending).Limit(500).Find(&pending).Error; err == nil {
		for _, task := range pending {
			if w.engine.hub.IsDeviceOnline(task.TargetDeviceID) {
				w.engine.store.EnqueueTask(ctx, task.ID)
			}
		}
	}
}

func (w *Worker) queueTimeout() time.Duration {
	if w.engine.cfg.QueueTimeoutSeconds > 0 {
		return time.Duration(w.engine.cfg.QueueTimeoutSeconds) * time.Second
	}
	return 5 * time.Second
}

func (w *Worker) lockTTL() time.Duration {
	if w.engine.cfg.LockTTLSeconds > 0 {
		return time.Duration(w.engine.cfg.LockTTLSeconds) * time.Second
	}
	return 300 * time.Second
}

func (w *Worker) taskTimeout() time.Duration {
	if w.engine.cfg.TaskTimeoutSeconds > 0 {
		return time.Duration(w.engine.cfg.TaskTimeoutSeconds) * time.Second
	}
	return 600 * time.Second
}
