package sync

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"syc-file/internal/model"
	"syc-file/internal/ws"
)

// lockKeyFor 构造按 用户/文件夹/相对路径 隔离的逻辑锁键，避免跨用户/跨文件夹误碰。
func lockKeyFor(t model.SyncTask) string {
	return fmt.Sprintf("%d:%d:%s", t.UserID, t.FolderID, t.RelativePath)
}

// isTerminalStatus 终态：已经结算过的任务不能再被完成/失败/阻塞（防重复扣 pending 计数）。
func isTerminalStatus(s string) bool {
	return s == model.SyncStatusCompleted || s == model.SyncStatusFailed || s == model.SyncStatusSkipped
}

// activeStatuses 未结算状态：可被复用/可被客户端结算。
func activeStatuses() []string {
	return []string{model.SyncStatusPending, model.SyncStatusSyncing, model.SyncStatusWaitingUnlock}
}

// releaseTaskLock 凭任务持有的令牌安全释放文件锁（令牌不匹配则不删）。
func (e *Engine) releaseTaskLock(ctx context.Context, t model.SyncTask) {
	token := derefStr(t.LockToken)
	if token == "" {
		return
	}
	_ = e.store.ReleaseFileLock(ctx, lockKeyFor(t), token)
}

// CompleteTask 任务完成：置 completed、扣减待办计数、释放文件锁、清进度。
//
// ⚠ 这里**不能只认 syncing**。客户端上线时会走 `GET /sync/tasks/pending` 把积压任务拉走自己执行
// （三端的离线追赶都这么干），那些任务还是 pending —— 曾经在这里直接 return，于是：
// 客户端干完活上报 complete → 服务端静默丢弃 → 任务永远 pending → Reaper 每 30s 捞出来重新派发
// → 客户端再干一遍再上报 → 永动机。表现就是 sync_task 表越积越多、日志里 `SELECT * FROM sync_task
// WHERE id = ?` 刷屏、设备越多越严重。凡是没结算过的状态都允许结算。
func (e *Engine) CompleteTask(taskID uint64, hash string) {
	var task model.SyncTask
	if err := e.db.First(&task, taskID).Error; err != nil {
		return
	}
	if isTerminalStatus(task.SyncStatus) {
		return
	}
	updates := map[string]interface{}{
		"sync_status":  model.SyncStatusCompleted,
		"progress":     100,
		"completed_at": nowPtr(),
		"lock_token":   nil,
	}
	if hash != "" {
		updates["file_hash"] = hash
	}
	e.db.Model(&task).Updates(updates)
	ctx := context.Background()
	e.store.DecPending(ctx, task.UserID)
	e.releaseTaskLock(ctx, task)
	e.store.ResetProgress(ctx, taskID)
}

// FailTask 任务失败：未超重试上限则回 pending 重新入队，否则置 failed。无论如何先释放锁。
func (e *Engine) FailTask(taskID uint64, errMsg string) {
	var task model.SyncTask
	if err := e.db.First(&task, taskID).Error; err != nil {
		return
	}
	// 同 CompleteTask：REST 拉走的 pending 任务失败也要能结算，否则重试次数永远涨不上去、永不收敛
	if isTerminalStatus(task.SyncStatus) {
		return
	}
	ctx := context.Background()
	e.releaseTaskLock(ctx, task)
	rc := task.RetryCount + 1
	if rc > task.MaxRetry {
		e.db.Model(&task).Updates(map[string]interface{}{
			"sync_status":   model.SyncStatusFailed,
			"error_message": errMsg,
			"completed_at":  nowPtr(),
			"retry_count":   rc,
			"lock_token":    nil,
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
		"lock_token":    nil,
	})
	e.store.EnqueueTask(ctx, taskID)
}

// BlockTask 目标文件被本地程序占用：转 waiting_unlock，释放锁但不增加重试次数，
// 等待文件释放后由 Reaper 重新派发。绝不强盖打开中的文档。
func (e *Engine) BlockTask(taskID uint64, reason string) {
	var task model.SyncTask
	if err := e.db.First(&task, taskID).Error; err != nil {
		return
	}
	if isTerminalStatus(task.SyncStatus) || task.SyncStatus == model.SyncStatusWaitingUnlock {
		return
	}
	ctx := context.Background()
	e.releaseTaskLock(ctx, task)
	if reason == "" {
		reason = "locked"
	}
	e.db.Model(&task).Updates(map[string]interface{}{
		"sync_status":   model.SyncStatusWaitingUnlock,
		"error_message": reason,
		"started_at":    nil,
		"lock_token":    nil,
	})
}

// UpdateTaskProgress 更新任务进度（DB + Redis 进度哈希）。
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

// ListTasks 按条件查询同步任务（供前端展示）。
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

// ListTasksPaged 分页查询任务记录，返回 (列表, 总数)。page 从 1 开始。
func (e *Engine) ListTasksPaged(userID uint, status, deviceID string, page, pageSize int) ([]model.SyncTask, int64, error) {
	q := e.db.Model(&model.SyncTask{}).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("sync_status = ?", status)
	}
	if deviceID != "" {
		q = q.Where("target_device_id = ? OR source_device_id = ?", deviceID, deviceID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	var ts []model.SyncTask
	if err := q.Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&ts).Error; err != nil {
		return nil, 0, err
	}
	return ts, total, nil
}

// clearableStatuses 允许批量清理/删除的终态。pending/syncing/waiting_unlock 是
// worker 的在管状态，删了会破坏调度，一律拒绝。
var clearableStatuses = map[string]bool{
	model.SyncStatusCompleted: true,
	model.SyncStatusFailed:    true,
}

// ClearTasks 批量删除该用户的终态任务记录（历史清理），返回删除条数。
// statuses 为空默认 completed+failed。
func (e *Engine) ClearTasks(userID uint, statuses []string) (int64, error) {
	valid := make([]string, 0, len(statuses))
	for _, s := range statuses {
		if clearableStatuses[s] {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		valid = []string{model.SyncStatusCompleted, model.SyncStatusFailed}
	}
	res := e.db.Where("user_id = ? AND sync_status IN ?", userID, valid).Delete(&model.SyncTask{})
	return res.RowsAffected, res.Error
}

// DeleteTask 删除单条终态任务记录。
func (e *Engine) DeleteTask(userID uint, id uint64) error {
	var t model.SyncTask
	if err := e.db.Where("id = ? AND user_id = ?", id, userID).First(&t).Error; err != nil {
		return fmt.Errorf("任务不存在")
	}
	if !clearableStatuses[t.SyncStatus] {
		return fmt.Errorf("仅可删除已完成/失败的记录")
	}
	return e.db.Delete(&t).Error
}

// PendingTasksForDevice 返回某设备待执行（pending）的任务，供 WS 不可用时 HTTP 拉取。
func (e *Engine) PendingTasksForDevice(userID uint, deviceID string) ([]model.SyncTask, error) {
	var ts []model.SyncTask
	if err := e.db.Where("user_id = ? AND target_device_id = ? AND sync_status = ?",
		userID, deviceID, model.SyncStatusPending).Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

// pushTaskCreated 通过 WS 把 task_created 推给目标设备，附带服务端绝对路径供其落盘。
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

func nowPtr() time.Time {
	return time.Now()
}
