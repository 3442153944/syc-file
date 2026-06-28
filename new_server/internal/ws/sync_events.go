package ws

const (
	SyncEventFileChanged      = "file_changed"
	SyncEventTaskCreated      = "task_created"
	SyncEventTaskProgress     = "task_progress"
	SyncEventTaskCompleted    = "task_completed"
	SyncEventTaskFailed       = "task_failed"
	SyncEventTaskBlocked      = "task_blocked"      // 目标文件被占用，转 waiting_unlock
	SyncEventConflict         = "conflict"          // 通知源设备隔离本地副本
	SyncEventConflictResolved = "conflict_resolved" // 待办处理结果回执
	SyncEventScanRequest      = "scan_request"
	SyncEventScanResult       = "scan_result"
)

type FileSyncMessageHandler func(conn *Connection, msg *Message, event string, content map[string]interface{})

var fileSyncHandler FileSyncMessageHandler

func SetFileSyncHandler(h FileSyncMessageHandler) {
	fileSyncHandler = h
}
