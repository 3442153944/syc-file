package model

import "time"

// FileVersion 文件版本历史表
type FileVersion struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FileID      uint64    `gorm:"not null;index" json:"file_id"`
	Version     int       `gorm:"not null" json:"version"`
	FileSize    *int64    `json:"file_size"`
	FileHash    *string   `gorm:"size:64" json:"file_hash"`
	StoragePath *string   `gorm:"size:1000" json:"storage_path"`
	CreatedBy   *uint     `json:"created_by"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (FileVersion) TableName() string { return "file_version" }
