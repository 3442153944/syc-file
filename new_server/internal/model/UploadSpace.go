package model

import "time"

// StoragePool 保存系统管理员在指定磁盘上创建的独立上传根目录。
// 根目录一旦建立便不可直接修改，避免已有文件失去索引或被静默遗留。
type StoragePool struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:100;not null" json:"name"`
	RootPath  string    `gorm:"size:500;not null;uniqueIndex" json:"root_path"`
	Enabled   bool      `gorm:"not null" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func (StoragePool) TableName() string { return "storage_pool" }

// UploadSpace 为每个账号（包括普通管理员）分配独立目录和容量。
// 未分配时拒绝普通上传；到期只冻结访问，不删除文件。
type UploadSpace struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	UserID      uint       `gorm:"not null;uniqueIndex" json:"user_id"`
	PoolID      uint       `gorm:"not null;index" json:"pool_id"`
	ParentID    *uint      `gorm:"index" json:"parent_id"`
	Directory   string     `gorm:"size:1500" json:"-"`
	AllocatedBy uint       `json:"allocated_by"`
	QuotaBytes  int64      `gorm:"not null" json:"quota_bytes"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Enabled     bool       `gorm:"not null" json:"enabled"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (UploadSpace) TableName() string { return "upload_space" }
