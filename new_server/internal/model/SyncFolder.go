package model

import "time"

type SyncFolder struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        uint      `gorm:"not null;index" json:"user_id"`
	Name          string    `gorm:"size:100" json:"name"`
	LocalPath     string    `gorm:"size:1000;not null" json:"local_path"`
	RemotePath    string    `gorm:"size:1000;not null;index" json:"remote_path"`
	Direction     string    `gorm:"size:20;not null;default:two_way" json:"direction"`
	Enabled       bool      `gorm:"not null;default:true;index" json:"enabled"`
	Excludes      string    `gorm:"type:text" json:"excludes"`
	OwnerDeviceID string    `gorm:"size:100" json:"owner_device_id"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SyncFolder) TableName() string { return "sync_folder" }
