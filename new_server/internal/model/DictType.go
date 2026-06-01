package model

import "time"

// DictType 字典类型表
type DictType struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	DictCode    string    `gorm:"size:50;not null;uniqueIndex" json:"dict_code"`
	DictName    string    `gorm:"size:100;not null" json:"dict_name"`
	Description *string   `gorm:"type:text" json:"description"`
	Status      int8      `gorm:"not null;default:1" json:"status"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (DictType) TableName() string { return "dict_type" }
