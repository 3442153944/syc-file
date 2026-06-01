package model

import "time"

// SyncTask 同步任务表
type SyncTask struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       uint       `gorm:"not null;index" json:"user_id"`
	DeviceID     uint       `gorm:"not null;index" json:"device_id"`
	FileID       uint64     `gorm:"not null;index" json:"file_id"`
	TaskType     string     `gorm:"size:20;not null" json:"task_type"`
	SyncStatus   string     `gorm:"size:20;not null;default:pending" json:"sync_status"`
	Progress     int        `gorm:"not null;default:0" json:"progress"`
	ErrorMessage *string    `gorm:"type:text" json:"error_message"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (SyncTask) TableName() string { return "sync_task" }
