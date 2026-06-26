package model

const (
	TaskTypeUpload   = "upload"
	TaskTypeDownload = "download"
	TaskTypeDelete   = "delete"
	TaskTypeMkdir    = "mkdir"
)

const (
	SyncStatusPending   = "pending"
	SyncStatusSyncing   = "syncing"
	SyncStatusCompleted = "completed"
	SyncStatusFailed    = "failed"
	SyncStatusSkipped   = "skipped"
	SyncStatusConflict  = "conflict"
)

const (
	DirectionTwoWay       = "two_way"
	DirectionUploadOnly   = "upload_only"
	DirectionDownloadOnly = "download_only"
)

const (
	FileChangeCreate = "create"
	FileChangeModify = "modify"
	FileChangeDelete = "delete"
)
