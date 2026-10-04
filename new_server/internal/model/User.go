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
	// Level 权限级别：0 游客 / 1 用户 / 2 管理员 / 3 超级管理员（常量见 level.go）。
	// 判定权限一律看 Level；Role 只为兼容客户端保留，两者必须同步（model.RoleOfLevel）。
	// ⚠ 默认值 1：用 gorm Create 写 Level=0（游客）会被当零值套成 1，必须随后显式 Update 成 0。
	Level int8 `gorm:"not null;default:1;index" json:"level"`
	// AllowRegister 是否开放自助注册。全局开关，落在超级管理员的行上（任一超级管理员为 true 即开放），
	// 对其它级别的行无意义。
	AllowRegister bool `gorm:"not null;default:false" json:"-"`
	// ExpiresAt 访客账号的过期时间；其它级别为空。
	ExpiresAt *time.Time `json:"expires_at"`
	// CreatedBy 访客由哪位管理员下发。
	CreatedBy *uint     `json:"created_by"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	// 粘贴快传：全局唤起快捷键，桌面端录制后编码成的十六进制数字（如 "0x108"），不是
	// "CommandOrControl+Shift+V" 这种平台相关的文本——避免和某个具体库/平台的按键语法绑死，
	// 桌面端自己按固定的编解码表（见 filesync-desktop 的 hotkey_codec.rs / hotkeyCodec.ts）
	// 转换成本地实际按键，Linux/Windows/macOS 三端都认同一份数字。
	QuickShareHotkey *string `gorm:"size:50" json:"quick_share_hotkey"`
	// 粘贴快传后分享链接的默认有效期（分钟），为空时用 config.yaml 的 quick_share.default_expire_minutes
	QuickShareExpireMinutes *int `json:"quick_share_expire_minutes"`
}

func (User) TableName() string { return "user" }
