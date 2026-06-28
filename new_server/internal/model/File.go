package model

import "time"

// File 文件表
type File struct {
	ID          uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint       `gorm:"not null;index:uk_file_user_path,unique,priority:1" json:"user_id"`
	ParentID    *uint64    `gorm:"index" json:"parent_id"`
	FileName    string     `gorm:"size:255;not null" json:"file_name"`
	FilePath    string     `gorm:"size:700;not null;index:uk_file_user_path,unique,priority:2" json:"file_path"`
	FileType    *string    `gorm:"size:20" json:"file_type"`
	FileSize    *int64     `json:"file_size"`
	FileHash    *string    `gorm:"size:64;index" json:"file_hash"`
	MimeType    *string    `gorm:"size:100" json:"mime_type"`
	IsDirectory bool       `gorm:"not null;default:false;index" json:"is_directory"`
	IsDeleted   bool       `gorm:"not null;default:false;index" json:"is_deleted"`
	Version     uint       `gorm:"not null;default:1" json:"version"`
	ShareCode   *string    `gorm:"size:32;index" json:"share_code"`
	ShareExpire *time.Time `json:"share_expire"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (File) TableName() string { return "file" }
