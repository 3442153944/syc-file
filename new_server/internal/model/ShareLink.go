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
	// 粘贴快传相关：IsQuickShare 标记该分享是否来自快传（而非手动分享已有文件/目录）；
	// StorageKind 标记内容存储位置："disk"=真实临时文件（旧数据/普通分享/快传落盘均是这个）、
	// "memory"=内容在 quickShareMemStore 里，没有真实文件。这两列不在 GORM AutoMigrate
	// 里（表由 sql/share_link.sql 手工建），改动需要手动执行 ALTER TABLE，且必须先于
	// 新二进制上线（否则连累已有的普通分享功能一起报"未知列"）。
	IsQuickShare bool   `gorm:"not null;default:0" json:"is_quick_share"`
	StorageKind  string `gorm:"size:10;not null;default:disk" json:"storage_kind"`
}

func (ShareLink) TableName() string { return "share_link" }
