package model

import "time"

// DSHToken DeepSeek Harness 访问令牌。
//
// 管理端只保存令牌的 SHA-256，明文只在创建时返回一次；兑换接口
// （GET /dsh/auth?t=...）用明文换 DSH 的浏览器会话 cookie。
//   - one_time：一次性登录短链，兑换一次后作废（used_at 非空即失效）；
//   - long_lived：长期 token，可重复兑换，直到过期或被吊销。
type DSHToken struct {
	ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	TokenHash  string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	Kind       string     `gorm:"size:16;not null" json:"kind"`
	Label      string     `gorm:"size:128" json:"label"`
	CreatedBy  uint       `gorm:"index" json:"created_by"`
	ExpiresAt  time.Time  `gorm:"not null;index" json:"expires_at"`
	UsedAt     *time.Time `json:"used_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (DSHToken) TableName() string { return "dsh_token" }
