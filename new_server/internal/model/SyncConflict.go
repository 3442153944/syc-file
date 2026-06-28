package model

import "time"

// SyncConflict 同步冲突待办记录。
//
// 当客户端上报的 base_hash 与服务端 trunk 当前 hash 不一致（并发分叉）时，
// 服务端不动 trunk，写入一条 pending 待办，并通知源设备把本地分叉副本隔离到
// .syncpending 目录。用户在「同步待办」里选择 accept_server / keep_local 解决。
//
// 冲突副本的字节保留在出冲突的客户端本地（.syncpending），服务端只存元数据。
type SyncConflict struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       uint   `gorm:"not null;index" json:"user_id"`
	DeviceID     string `gorm:"size:100;not null;index" json:"device_id"` // 出冲突的源设备
	FolderID     uint64 `gorm:"index" json:"folder_id"`
	FileID       uint64 `gorm:"index" json:"file_id"`
	RelativePath string `gorm:"size:1000;not null" json:"relative_path"`
	FileName     string `gorm:"size:255;not null" json:"file_name"`

	ServerHash    *string `gorm:"size:64" json:"server_hash"` // 冲突时 trunk 当前 hash
	LocalHash     *string `gorm:"size:64" json:"local_hash"`  // 客户端分叉版本 hash
	BaseHash      *string `gorm:"size:64" json:"base_hash"`   // 客户端修改前的 base
	ServerVersion uint    `gorm:"not null;default:0" json:"server_version"`

	Status     string     `gorm:"size:20;not null;default:pending;index" json:"status"` // pending/resolved
	Resolution *string    `gorm:"size:20" json:"resolution"`                            // accept_server/keep_local
	ResolvedAt *time.Time `json:"resolved_at"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SyncConflict) TableName() string { return "sync_conflict" }
