package model

import "time"

// DictData 字典数据表
type DictData struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	DictTypeID uint      `gorm:"not null;index" json:"dict_type_id"`
	DictLabel  string    `gorm:"size:100;not null" json:"dict_label"`
	DictValue  string    `gorm:"size:100;not null;index" json:"dict_value"`
	DictSort   int       `gorm:"not null;default:0;index" json:"dict_sort"`
	CssClass   *string   `gorm:"size:50" json:"css_class"`
	TagType    *string   `gorm:"size:20" json:"tag_type"`
	Remark     *string   `gorm:"type:text" json:"remark"`
	Status     int8      `gorm:"not null;default:1" json:"status"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (DictData) TableName() string { return "dict_data" }
