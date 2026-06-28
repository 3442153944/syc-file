package model

// 任务类型
const (
	TaskTypeUpload   = "upload"
	TaskTypeDownload = "download"
	TaskTypeDelete   = "delete"
	TaskTypeMkdir    = "mkdir"
)

// 同步任务状态
const (
	SyncStatusPending       = "pending"        // 待派发
	SyncStatusSyncing       = "syncing"        // 派发中/执行中
	SyncStatusCompleted     = "completed"      // 完成
	SyncStatusFailed        = "failed"         // 失败（已耗尽重试）
	SyncStatusSkipped       = "skipped"        // 跳过
	SyncStatusConflict      = "conflict"       // 冲突（兼容旧字段，新流程走 sync_conflict 表）
	SyncStatusWaitingUnlock = "waiting_unlock" // 目标文件被占用，长期等待且不计重试
)

// 同步方向
const (
	DirectionTwoWay       = "two_way"
	DirectionUploadOnly   = "upload_only"
	DirectionDownloadOnly = "download_only"
)

// 文件变更动作
const (
	FileChangeCreate = "create"
	FileChangeModify = "modify"
	FileChangeDelete = "delete"
)

// 冲突待办状态
const (
	ConflictStatusPending  = "pending"
	ConflictStatusResolved = "resolved"
)

// 冲突解决方式
const (
	ResolutionAcceptServer = "accept_server" // 接受服务端版本，丢弃本地隔离副本
	ResolutionKeepLocal    = "keep_local"    // 保留本地版本，以当前 trunk 为 base 重新提交
)
