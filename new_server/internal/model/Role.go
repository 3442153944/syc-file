package model

import "time"

// Role 角色表
type Role struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	RoleCode    string    `gorm:"size:50;not null;uniqueIndex" json:"role_code"`
	RoleName    string    `gorm:"size:100;not null" json:"role_name"`
	Description *string   `gorm:"type:text" json:"description"`
	Status      int8      `gorm:"not null;default:1" json:"status"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Role) TableName() string { return "role" }
