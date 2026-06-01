package model

import "time"

// ShareRecord 分享记录表
type ShareRecord struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        uint       `gorm:"not null;index" json:"user_id"`
	FileID        uint64     `gorm:"not null" json:"file_id"`
	ShareCode     string     `gorm:"size:32;not null;uniqueIndex" json:"share_code"`
	SharePassword *string    `gorm:"size:20" json:"share_password"`
	ExpireTime    *time.Time `json:"expire_time"`
	DownloadLimit *int       `json:"download_limit"`
	DownloadCount int        `gorm:"not null;default:0" json:"download_count"`
	VisitCount    int        `gorm:"not null;default:0" json:"visit_count"`
	Status        int8       `gorm:"not null;default:1" json:"status"`
	CreatedAt     time.Time  `gorm:"autoCreateTime;index" json:"created_at"`
}

func (ShareRecord) TableName() string { return "share_record" }
