package model

import "time"

// Permission 权限表
type Permission struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	PermissionCode string    `gorm:"size:50;not null;uniqueIndex" json:"permission_code"`
	PermissionName string    `gorm:"size:100;not null" json:"permission_name"`
	ParentID       *uint     `gorm:"index" json:"parent_id"`
	PermissionType *string   `gorm:"size:20" json:"permission_type"`
	Description    *string   `gorm:"type:text" json:"description"`
	SortOrder      int       `gorm:"not null;default:0" json:"sort_order"`
	Status         int8      `gorm:"not null;default:1" json:"status"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (Permission) TableName() string { return "permission" }
