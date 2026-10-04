package model

import "time"

// 权限级别（User.Level）。值越大权限越高，判定一律用「>= 某级」。
//
// role 字符串保持原样（user / admin / guest）以兼容三端客户端和既有的 role=="admin" 判断：
// 超级管理员与管理员的 role 都是 admin，区分只看 Level。
const (
	LevelGuest int8 = 0 // 游客：管理员下发的临时账号，只能访问被指定的页面 / 接口
	LevelUser  int8 = 1 // 用户
	LevelAdmin int8 = 2 // 管理员
	LevelSuper int8 = 3 // 超级管理员
)

// RoleOfLevel level → 兼容用的 role 字符串。改 Level 时必须同步写 role，保持两者一致。
func RoleOfLevel(l int8) string {
	switch {
	case l >= LevelAdmin:
		return "admin"
	case l == LevelUser:
		return "user"
	default:
		return "guest"
	}
}

// AppSetting 通用的服务端键值设置（初始化标识等）。注意：不是密钥表，别往里放机密。
type AppSetting struct {
	Name  string `gorm:"primaryKey;size:64"`
	Value string `gorm:"type:text;not null"`
}

func (AppSetting) TableName() string { return "app_setting" }

// Route 服务器下发给客户端的路由 / 菜单条目。
//
// 客户端（桌面端与 Web 端共用一套前端）只内置登录、注册、初始化等基础路由，其余全部按这张表动态注册。
// 页面代码仍在客户端里，Component 是客户端「页面注册表」中的 key，服务器能控制的是：
// 有哪些入口、顺序、标题、图标、谁能进，而不是凭空新增一个客户端没有的页面。
// 找不到 Component 的条目，客户端直接忽略（老客户端遇到新条目不会崩）。
//
// 表由内置目录（internal/system/catalog.go）按 Code「缺失才插入」播种，之后管理员的修改不会被覆盖。
type Route struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Code       string    `gorm:"size:64;not null;uniqueIndex" json:"code"`
	ParentCode string    `gorm:"size:64;not null;default:'';index" json:"parent_code"`
	Path       string    `gorm:"size:200;not null" json:"path"`
	Name       string    `gorm:"size:64;not null;default:''" json:"name"`       // vue-router 的路由名
	Component  string    `gorm:"size:120;not null;default:''" json:"component"` // 空 = 纯菜单分组（目录）
	Title      string    `gorm:"size:64;not null" json:"title"`
	Icon       string    `gorm:"size:32;not null;default:''" json:"icon"`
	Sort       int       `gorm:"not null;default:0" json:"sort"`
	Hidden     bool      `gorm:"not null;default:false" json:"hidden"`    // true = 可进入但不在菜单里显示
	MinLevel   int8      `gorm:"not null;default:1" json:"min_level"`     // 登录用户（level>=1）的最低可见级别
	Perm       string    `gorm:"size:64;not null;default:''" json:"perm"` // 游客可访问该页面时放行的接口分组（见 system/guest_perm.go）
	Enabled    bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Route) TableName() string { return "sys_route" }

// UserRoute 游客账号被授权访问的页面（按路由 Code）。只对 level=0 的账号有意义。
type UserRoute struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:uk_user_route" json:"user_id"`
	RouteCode string    `gorm:"size:64;not null;uniqueIndex:uk_user_route" json:"route_code"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (UserRoute) TableName() string { return "user_route" }
