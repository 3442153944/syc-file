package sync

import (
	"context"
	"time"

	"go.uber.org/zap"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// Worker 从 Redis 队列取任务，抢文件锁后把 task_created 推给目标设备。
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

// processTask 取任务 → 校验状态/在线 → 抢文件锁（按路径串行）→ 置 syncing 并推送。
// pending 与 waiting_unlock 都可被拾取（后者是等待文件释放的重试）。
func (w *Worker) processTask(taskID uint64) {
	var task model.SyncTask
	if err := w.engine.db.First(&task, taskID).Error; err != nil {
		logger.Logger.Error("同步任务不存在", zap.Uint64("task_id", taskID), zap.Error(err))
		return
	}
	if task.SyncStatus != model.SyncStatusPending && task.SyncStatus != model.SyncStatusWaitingUnlock {
		return
	}
	if !w.engine.hub.IsDeviceOnline(task.TargetDeviceID) {
		return
	}

	ctx := context.Background()
	key := lockKeyFor(task)
	token, ok, err := w.engine.store.AcquireFileLock(ctx, key, w.effectiveLockTTL())
	if err != nil {
		logger.Logger.Error("获取文件锁失败", zap.Uint64("task_id", taskID), zap.Error(err))
		return
	}
	if !ok {
		// 同一路径正被另一任务处理：短暂退避后重新入队，避免热自旋。
		time.Sleep(time.Second)
		w.engine.store.EnqueueTask(ctx, taskID)
		return
	}

	now := time.Now()
	if err := w.engine.db.Model(&task).Updates(map[string]interface{}{
		"sync_status": model.SyncStatusSyncing,
		"started_at":  now,
		"progress":    0,
		"lock_token":  token,
	}).Error; err != nil {
		w.engine.store.ReleaseFileLock(ctx, key, token)
		return
	}
	task.LockToken = &token

	var folder model.SyncFolder
	if err := w.engine.db.First(&folder, task.FolderID).Error; err != nil {
		logger.Logger.Warn("同步任务关联文件夹缺失", zap.Uint64("folder_id", task.FolderID))
	} else {
		w.engine.pushTaskCreated(&task, folder)
	}
}

// Reaper 周期性兜底：重试超时的 syncing 任务，补发在线设备的 pending/waiting_unlock 积压。
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

	// 1) 超时仍 syncing 的任务：释放锁后重试或判失败。
	var stuck []model.SyncTask
	if err := w.engine.db.Where("sync_status = ? AND started_at < ?", model.SyncStatusSyncing, cutoff).Limit(500).Find(&stuck).Error; err == nil {
		for _, task := range stuck {
			w.engine.releaseTaskLock(ctx, task)
			rc := task.RetryCount + 1
			if rc > task.MaxRetry {
				w.engine.db.Model(&task).Updates(map[string]interface{}{
					"sync_status":   model.SyncStatusFailed,
					"error_message": "task timeout",
					"completed_at":  now,
					"retry_count":   rc,
					"lock_token":    nil,
				})
				w.engine.store.DecPending(ctx, task.UserID)
				continue
			}
			w.engine.db.Model(&task).Updates(map[string]interface{}{
				"sync_status": model.SyncStatusPending,
				"retry_count": rc,
				"started_at":  nil,
				"lock_token":  nil,
			})
			w.engine.store.EnqueueTask(ctx, task.ID)
		}
	}

	// 2) pending / waiting_unlock：目标在线则重新入队（补离线积压、重试等待解锁）。
	var revivable []model.SyncTask
	statuses := []string{model.SyncStatusPending, model.SyncStatusWaitingUnlock}
	if err := w.engine.db.Where("sync_status IN ?", statuses).Limit(500).Find(&revivable).Error; err == nil {
		for _, task := range revivable {
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

// effectiveLockTTL 保证文件锁 TTL 不短于任务超时（再加缓冲），
// 避免锁在任务执行中途过期、其它任务并发抢同一路径造成冲突。
func (w *Worker) effectiveLockTTL() time.Duration {
	ttl := w.lockTTL()
	if t := w.taskTimeout(); t > ttl {
		ttl = t
	}
	return ttl + 60*time.Second
}
