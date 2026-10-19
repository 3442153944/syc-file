package vpn

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
)

// AllocateSubnet 从地址池里切一个空闲的 /24 给新建的组。
//
// 池默认 172.1.0.0/16，从池起点顺序扫描；已用网段按字符串精确比对，
// 因此手工指定的网段（只要在池内）也会被跳过。
func AllocateSubnet(pool string, used []string) (string, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(pool))
	if err != nil {
		return "", fmt.Errorf("地址池格式非法: %w", err)
	}
	if !p.Addr().Is4() {
		return "", fmt.Errorf("地址池必须是 IPv4")
	}
	if p.Bits() > 24 {
		return "", fmt.Errorf("地址池 %s 太小：至少要 /24 才能切分出组网段", pool)
	}

	usedSet := make(map[string]bool, len(used))
	for _, s := range used {
		usedSet[strings.TrimSpace(s)] = true
	}

	base := p.Masked().Addr().As4()
	start := uint64(binary.BigEndian.Uint32(base[:]))
	span := uint64(1) << (32 - p.Bits())

	for off := uint64(0); off < span; off += 256 {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(start+off))
		candidate := fmt.Sprintf("%s/24", netip.AddrFrom4(raw).String())
		if !usedSet[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("地址池 %s 已无空闲 /24", pool)
}

// SubnetInPool 判断组网段是否落在地址池内（且不宽于池本身）。
func SubnetInPool(subnet, pool string) bool {
	s, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil || !s.Addr().Is4() {
		return false
	}
	p, err := netip.ParsePrefix(strings.TrimSpace(pool))
	if err != nil || !p.Addr().Is4() {
		return false
	}
	if s.Bits() < p.Bits() {
		return false
	}
	return p.Contains(s.Masked().Addr())
}
