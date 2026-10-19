package model

import "time"

// VPN 组网（云梯组网）数据模型。
//
// 设计约束（见 docs/mesh-vpn-design.md）：
//   - 密钥只入库，服务器不生成 wg0.conf，也不把密钥写进 config.yaml；
//   - 私钥字段存 AES-256-GCM 密文，AAD 绑定 device_id；
//   - WireGuard 的公钥/私钥都是 32 字节，base64 后 44 字符。

const (
	VPNRoleMember     = "member"
	VPNRoleGroupAdmin = "group_admin"
	VPNRoleOrgAdmin   = "org_admin"

	VPNKeySourceServer = "server" // 服务端生成并托管密文私钥
	VPNKeySourceClient = "client" // 客户端生成，服务端只存公钥

	VPNDeviceActive  = "active"
	VPNDeviceRevoked = "revoked"

	VPNInteropAdminsOnly = "admins_only"
	VPNInteropFull       = "full"
)

// VPNOrg 机构：ACL 的最外层作用域，跨机构永不互通。
type VPNOrg struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"size:100;not null;uniqueIndex" json:"name"`
	OwnerUserID uint      `gorm:"not null;index" json:"owner_user_id"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (VPNOrg) TableName() string { return "vpn_org" }

// VPNGroup 二级组：一个三层网段（如 10.12.3.0/24），组内成员拿到该网段的 /32。
type VPNGroup struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	OrgID         uint      `gorm:"not null;index;uniqueIndex:uk_vpn_group_name" json:"org_id"`
	Name          string    `gorm:"size:100;not null;uniqueIndex:uk_vpn_group_name" json:"name"`
	Subnet        string    `gorm:"size:64;not null" json:"subnet"`
	GatewayUserID *uint     `json:"gateway_user_id"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (VPNGroup) TableName() string { return "vpn_group" }

// VPNMember 用户在某个组里的身份。同一用户可以在多个组里各有一条记录。
type VPNMember struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	GroupID   uint      `gorm:"not null;index;uniqueIndex:uk_vpn_member" json:"group_id"`
	UserID    uint      `gorm:"not null;index;uniqueIndex:uk_vpn_member" json:"user_id"`
	Role      string    `gorm:"size:20;not null;default:member" json:"role"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (VPNMember) TableName() string { return "vpn_member" }

// VPNDevice 一台加入组网的设备。privkey_enc 为空表示私钥只在客户端。
type VPNDevice struct {
	ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint       `gorm:"not null;index;uniqueIndex:uk_vpn_device" json:"user_id"`
	GroupID    uint       `gorm:"not null;index" json:"group_id"`
	DeviceID   string     `gorm:"size:100;not null;uniqueIndex:uk_vpn_device" json:"device_id"`
	PubKey     string     `gorm:"size:64;not null;uniqueIndex" json:"pubkey"`
	PrivKeyEnc string     `gorm:"size:255" json:"-"`
	KeySource  string     `gorm:"size:16;not null;default:client" json:"key_source"`
	Address    string     `gorm:"size:64;not null" json:"address"`
	Endpoint   string     `gorm:"size:128" json:"endpoint"`
	LastSeen   *time.Time `json:"last_seen"`
	Status     string     `gorm:"size:16;not null;default:active;index" json:"status"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (VPNDevice) TableName() string { return "vpn_device" }

// VPNEnrollToken 一次性入组令牌。只存哈希，令牌本身只在签发响应里出现一次。
type VPNEnrollToken struct {
	ID        uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint       `gorm:"not null;index" json:"user_id"`
	GroupID   uint       `gorm:"not null;index" json:"group_id"`
	TokenHash string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ExpiresAt time.Time  `gorm:"not null;index" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (VPNEnrollToken) TableName() string { return "vpn_enroll_token" }

// VPNInterop 超管按组对开关的跨组互通策略，默认关闭，双向生效。
type VPNInterop struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	OrgID     uint      `gorm:"not null;index" json:"org_id"`
	AGroupID  uint      `gorm:"not null;index;uniqueIndex:uk_vpn_interop" json:"a_group_id"`
	BGroupID  uint      `gorm:"not null;index;uniqueIndex:uk_vpn_interop" json:"b_group_id"`
	Scope     string    `gorm:"size:20;not null;default:admins_only" json:"scope"`
	Enabled   bool      `gorm:"not null;default:false" json:"enabled"`
	CreatedBy uint      `gorm:"not null" json:"created_by"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (VPNInterop) TableName() string { return "vpn_interop" }

// VPNDeviceHistory 已轮换/吊销的设备公钥，保留用于审计与排查。
type VPNDeviceHistory struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint      `gorm:"not null;index" json:"user_id"`
	DeviceID  string    `gorm:"size:100;not null;index" json:"device_id"`
	PubKey    string    `gorm:"size:64;not null" json:"pubkey"`
	Reason    string    `gorm:"size:32" json:"reason"`
	RetiredAt time.Time `gorm:"autoCreateTime" json:"retired_at"`
}

func (VPNDeviceHistory) TableName() string { return "vpn_device_history" }
