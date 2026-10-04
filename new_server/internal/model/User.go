package model

import "time"

// User 用户表
type User struct {
	ID        uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	Username  string     `gorm:"size:50;not null;uniqueIndex" json:"username"`
	Password  string     `gorm:"size:255;not null" json:"-"`
	Email     *string    `gorm:"size:100;uniqueIndex" json:"email"`
	Phone     *string    `gorm:"size:20" json:"phone"`
	Avatar    *string    `gorm:"size:255" json:"avatar"`
	Role      string     `gorm:"size:20;not null;default:user" json:"role"`
	Status    int8       `gorm:"not null;default:1" json:"status"`
	LastLogin *time.Time `json:"last_login"`
	CreatedAt time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	// 粘贴快传：全局唤起快捷键，桌面端录制后编码成的十六进制数字（如 "0x108"），不是
	// "CommandOrControl+Shift+V" 这种平台相关的文本——避免和某个具体库/平台的按键语法绑死，
	// 桌面端自己按固定的编解码表（见 filesync-desktop 的 hotkey_codec.rs / hotkeyCodec.ts）
	// 转换成本地实际按键，Linux/Windows/macOS 三端都认同一份数字。
	QuickShareHotkey *string `gorm:"size:50" json:"quick_share_hotkey"`
	// 粘贴快传后分享链接的默认有效期（分钟），为空时用 config.yaml 的 quick_share.default_expire_minutes
	QuickShareExpireMinutes *int `json:"quick_share_expire_minutes"`
}

func (User) TableName() string { return "user" }
