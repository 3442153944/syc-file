package model

import "time"

// VpsNode 一台 VPS 节点的面板入口登记。
//
// 背景：节点的 x-ui 面板原本直接对公网开端口，被扫到就会被爆破。现在改成
// 「每条记录自带一个随机入口路径 + 一把访问秘钥」，npx 层的 nginx 只放行
// 带正确秘钥的入口请求，命中后种 cookie 再跳转到面板真实路径。
//
//   - AccessPath：随机入口路径，形如 /p/3f9a1c22b7d0/，唯一；
//   - AccessKeyEnc：访问秘钥的密文（pkg/token 的 AES-GCM 盒），签发链接时要回读明文；
//   - PanelPath：面板自己的 webBasePath（x-ui 设置里的那个），入口校验通过后跳到这里；
//   - GatewayHost：入口域名，留空则用 Host。
//
// 状态与流量是后端定时/手动探测的结果，只作展示，不参与鉴权。
type VpsNode struct {
	ID       uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Code     string `gorm:"size:32;not null;uniqueIndex" json:"code"` // jp / ny / hk / aws
	Title    string `gorm:"size:64;not null" json:"title"`
	Region   string `gorm:"size:32" json:"region"`
	Provider string `gorm:"size:32" json:"provider"`

	Host        string `gorm:"size:128;not null" json:"host"`                     // 例：jp.sunyuanling.cn
	GatewayHost string `gorm:"size:128" json:"gateway_host"`                      // 入口域名，空则同 Host
	PanelScheme string `gorm:"size:8;not null;default:https" json:"panel_scheme"` // http / https
	PanelPort   int    `gorm:"not null;default:443" json:"panel_port"`            // 面板监听端口
	PanelPath   string `gorm:"size:128;not null;default:/" json:"panel_path"`     // 面板 webBasePath
	GatewayPort int    `gorm:"not null;default:443" json:"gateway_port"`          // 入口对外端口（PanelPath 为 / 时用它做独立 server）

	AccessPath   string `gorm:"size:128;not null;uniqueIndex" json:"access_path"` // 随机入口路径
	AccessKeyEnc string `gorm:"size:255;not null" json:"-"`                       // 秘钥密文，绝不出接口

	// SubURL 订阅地址：
	//   - 以 http(s):// 开头 = 完整地址，后端直接取（要求该地址对后端可达）；
	//   - 以 / 开头 = 面板侧订阅路径（如 /sub/abc/），由同一个网关按 cookie 放行，
	//     后端带着入口 cookie 去取，这样订阅端口可以继续只监听回环。
	SubURL  string `gorm:"size:255" json:"sub_url"`
	SubPort int    `gorm:"not null;default:2096" json:"sub_port"` // 面板侧订阅端口（仅在 SubURL 为相对路径时用于生成片段）
	Remark  string `gorm:"size:255" json:"remark"`                // 备注
	Enabled bool   `gorm:"not null;default:true" json:"enabled"`
	Sort    int    `gorm:"not null;default:0" json:"sort"`

	LastStatus    string     `gorm:"size:16;not null;default:unknown" json:"last_status"` // ok / fail / unknown
	LastLatencyMs int        `json:"last_latency_ms"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastError     string     `gorm:"size:255" json:"last_error"`

	TrafficUp     int64      `json:"traffic_up"`    // bytes
	TrafficDown   int64      `json:"traffic_down"`  // bytes
	TrafficTotal  int64      `json:"traffic_total"` // bytes，0 = 不限
	TrafficExpire *time.Time `json:"traffic_expire"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (VpsNode) TableName() string { return "vps_node" }
