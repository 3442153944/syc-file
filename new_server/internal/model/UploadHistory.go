package model

import "time"

type UploadHistory struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       uint       `gorm:"not null;index" json:"user_id"`
	FileName     string     `gorm:"size:255;not null;index" json:"file_name"`
	OriginalName *string    `gorm:"size:255" json:"original_name"`
	FileSize     *int64     `json:"file_size"`
	FileType     *string    `gorm:"size:100" json:"file_type"`
	StoragePath  *string    `gorm:"size:500" json:"storage_path"`
	UploadID     *string    `gorm:"size:64;index" json:"upload_id"`   // 分片上传会话 id（单发上传为空）
	ChunkCount   *int       `json:"chunk_count"`                       // 分片总数（单发上传为空）
	UploadStatus string     `gorm:"size:20;not null;default:pending" json:"upload_status"`
	UploadSpeed  *int64     `json:"upload_speed"`
	Progress     int8       `gorm:"not null;default:0" json:"progress"`
	IPAddress    *string    `gorm:"size:50" json:"ip_address"`
	UserAgent    *string    `gorm:"size:500" json:"user_agent"`
	ErrorMessage *string    `gorm:"type:text" json:"error_message"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (UploadHistory) TableName() string { return "upload_history" }
