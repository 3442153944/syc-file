package model

import "time"

type SyncTask struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID         uint       `gorm:"not null;index" json:"user_id"`
	SourceDeviceID string     `gorm:"size:100;not null;index" json:"source_device_id"`
	TargetDeviceID string     `gorm:"size:100;not null;index" json:"target_device_id"`
	FolderID       uint64     `gorm:"index" json:"folder_id"`
	FileID         uint64     `gorm:"index" json:"file_id"`
	TaskType       string     `gorm:"size:20;not null" json:"task_type"`
	SyncStatus     string     `gorm:"size:20;not null;default:pending;index" json:"sync_status"`
	Direction      string     `gorm:"size:20;not null;default:download" json:"direction"`
	RelativePath   string     `gorm:"size:1000;not null" json:"relative_path"`
	FileName       string     `gorm:"size:255;not null" json:"file_name"`
	FileSize       int64      `gorm:"not null;default:0" json:"file_size"`
	FileHash       *string    `gorm:"size:64;index" json:"file_hash"`
	SourceHash     *string    `gorm:"size:64" json:"source_hash"`
	Conflict       bool       `gorm:"not null;default:false;index" json:"conflict"`
	Progress       int        `gorm:"not null;default:0" json:"progress"`
	Priority       int        `gorm:"not null;default:0" json:"priority"`
	RetryCount     int        `gorm:"not null;default:0" json:"retry_count"`
	MaxRetry       int        `gorm:"not null;default:3" json:"max_retry"`
	ErrorMessage   *string    `gorm:"type:text" json:"error_message"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SyncTask) TableName() string { return "sync_task" }
