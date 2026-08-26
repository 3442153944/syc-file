package model

import "time"

// ShareLink 分享链接表：记录每次创建分享链接的元信息，硬链接临时文件由 share_link 生命周期管理。
type ShareLink struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       uint       `gorm:"not null;index" json:"user_id"`
	ShareCode    string     `gorm:"size:36;not null;uniqueIndex" json:"share_code"`
	OriginalPath string     `gorm:"size:1000;not null" json:"original_path"`
	FileName     string     `gorm:"size:255;not null" json:"file_name"`
	TempName     string     `gorm:"size:255;not null" json:"temp_name"`
	FileSize     int64      `gorm:"not null;default:0" json:"file_size"`
	ExpireTime   time.Time  `gorm:"not null;index" json:"expire_time"`
	Status       int8       `gorm:"not null;default:1" json:"status"` // 1=有效 0=自然到期 2=创建者主动吊销
	ExpiredAt    *time.Time `json:"expired_at"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (ShareLink) TableName() string { return "share_link" }
