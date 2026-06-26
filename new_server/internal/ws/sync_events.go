package ws

const (
	SyncEventFileChanged   = "file_changed"
	SyncEventTaskCreated   = "task_created"
	SyncEventTaskProgress  = "task_progress"
	SyncEventTaskCompleted = "task_completed"
	SyncEventTaskFailed    = "task_failed"
	SyncEventConflict      = "conflict"
	SyncEventScanRequest   = "scan_request"
	SyncEventScanResult    = "scan_result"
)

type FileSyncMessageHandler func(conn *Connection, msg *Message, event string, content map[string]interface{})

var fileSyncHandler FileSyncMessageHandler

func SetFileSyncHandler(h FileSyncMessageHandler) {
	fileSyncHandler = h
}
