package model

import "time"

// RolePermission 角色权限关联表
type RolePermission struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	RoleID       uint      `gorm:"not null;index" json:"role_id"`
	PermissionID uint      `gorm:"not null;index" json:"permission_id"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (RolePermission) TableName() string { return "role_permission" }
