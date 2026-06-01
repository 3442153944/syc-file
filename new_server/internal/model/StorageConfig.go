package model

import "time"

// StorageConfig 存储配置表
type StorageConfig struct {
	ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint       `gorm:"not null;uniqueIndex" json:"user_id"`
	TotalQuota int64      `gorm:"not null;default:10737418240" json:"total_quota"`
	UsedQuota  int64      `gorm:"not null;default:0" json:"used_quota"`
	FileCount  int        `gorm:"not null;default:0" json:"file_count"`
	LastSync   *time.Time `json:"last_sync"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (StorageConfig) TableName() string { return "storage_config" }
