package vpn

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"

	"syc-file/internal/model"
)

// DeviceRef 是计算对端可见性时的最小快照，来自 vpn_device 与 vpn_group 的联表，
// 不依赖 gin / gorm，便于直接单测。
type DeviceRef struct {
	DeviceID string
	PubKey   string
	Address  string
	Endpoint string
	UserID   uint
	GroupID  uint
	OrgID    uint
	Role     string // 该用户在该组的角色
	Status   string
}

// VisiblePeers 返回 self 能看到并与之建隧道的设备（不含自己）。
//
// 规则：
//  1. 同组互通；
//  2. org_admin 可穿透本机构所有组；
//  3. 跨组互通由 vpn_interop 控制，默认无记录即隔离；
//  4. 跨机构永不互通（先按 OrgID 过滤）。
func VisiblePeers(self DeviceRef, all []DeviceRef, interops []model.VPNInterop) []DeviceRef {
	out := make([]DeviceRef, 0, len(all))
	for _, peer := range all {
		if peer.DeviceID == self.DeviceID {
			continue
		}
		if peer.Status != "" && peer.Status != model.VPNDeviceActive {
			continue
		}
		if peer.OrgID != self.OrgID {
			continue
		}
		if peerVisible(self, peer, interops) {
			out = append(out, peer)
		}
	}
	return out
}

func peerVisible(self, peer DeviceRef, interops []model.VPNInterop) bool {
	if peer.GroupID == self.GroupID {
		return true
	}
	if self.Role == model.VPNRoleOrgAdmin {
		return true
	}
	// WireGuard 握手是双向的：只让机构管理员看到别人、别人看不到它，
	// 结果就是管理员发得起包、对端却无法完成握手。所以机构管理员设备
	// 必须对同机构所有人可见，穿透才真的成立。
	if peer.Role == model.VPNRoleOrgAdmin {
		return true
	}
	for _, it := range interops {
		if !it.Enabled || it.OrgID != self.OrgID {
			continue
		}
		if !pairMatches(it, self.GroupID, peer.GroupID) {
			continue
		}
		switch it.Scope {
		case model.VPNInteropFull:
			return true
		case model.VPNInteropAdminsOnly:
			if IsGroupAdmin(self.Role) && IsGroupAdmin(peer.Role) {
				return true
			}
		}
	}
	return false
}

func pairMatches(it model.VPNInterop, a, b uint) bool {
	return (it.AGroupID == a && it.BGroupID == b) || (it.AGroupID == b && it.BGroupID == a)
}

// IsGroupAdmin 组管理员与机构管理员都算“管理员”，用于 admins_only 互通。
func IsGroupAdmin(role string) bool {
	return role == model.VPNRoleGroupAdmin || role == model.VPNRoleOrgAdmin
}

// AllowedIPsOf 把设备地址转成 WireGuard 的 /32 白名单项。
func AllowedIPsOf(address string) []string {
	if address == "" {
		return nil
	}
	return []string{address + "/32"}
}

// ValidateSubnet 校验组网段：IPv4、前缀不超过 30、地址是网络地址（10.12.3.0/24 这种）。
func ValidateSubnet(subnet string) error {
	p, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return fmt.Errorf("网段格式非法: %w", err)
	}
	if !p.Addr().Is4() {
		return fmt.Errorf("当前只支持 IPv4 网段")
	}
	if p.Bits() > 30 {
		return fmt.Errorf("网段至少要 /30，否则没有可用主机")
	}
	if p.Addr() != p.Masked().Addr() {
		return fmt.Errorf("请使用网络地址形式，例如 %s", p.Masked().String())
	}
	return nil
}

// NextAddress 在组网段内分配下一个可用地址。
// 只支持 IPv4 且前缀长度不超过 30，避免 /31、/32 这类没有可用主机的配置。
func NextAddress(subnet string, used map[string]bool) (string, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return "", fmt.Errorf("网段格式非法: %w", err)
	}
	if !p.Addr().Is4() {
		return "", fmt.Errorf("当前只支持 IPv4 网段")
	}
	ones := p.Bits()
	if ones > 30 {
		return "", fmt.Errorf("网段 %s 没有可用主机地址", subnet)
	}

	base := p.Masked().Addr().As4()
	network := binary.BigEndian.Uint32(base[:])
	mask := uint32(0)
	if ones > 0 {
		mask = ^uint32(0) << (32 - ones)
	}
	broadcast := network | ^mask
	if broadcast-network <= 2 {
		return "", fmt.Errorf("网段 %s 没有可用主机地址", subnet)
	}

	for candidate := network + 1; candidate < broadcast; candidate++ {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], candidate)
		ip := netip.AddrFrom4(raw).String()
		if !used[ip] {
			return ip, nil
		}
	}
	return "", fmt.Errorf("网段 %s 已无可用地址", subnet)
}
