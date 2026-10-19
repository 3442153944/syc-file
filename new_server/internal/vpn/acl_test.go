package vpn

import (
	"testing"

	"syc-file/internal/model"
)

func dev(deviceID string, groupID, userID uint, role string) DeviceRef {
	return DeviceRef{
		DeviceID: deviceID, GroupID: groupID, UserID: userID, Role: role,
		OrgID: 1, Status: model.VPNDeviceActive,
		PubKey: "pk-" + deviceID, Address: "10.0.0.1",
	}
}

func ids(refs []DeviceRef) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, r := range refs {
		out[r.DeviceID] = true
	}
	return out
}

func TestSameGroupVisible(t *testing.T) {
	self := dev("a", 10, 1, model.VPNRoleMember)
	all := []DeviceRef{self, dev("b", 10, 2, model.VPNRoleMember), dev("c", 11, 3, model.VPNRoleMember)}
	got := ids(VisiblePeers(self, all, nil))
	if !got["b"] || got["c"] || got["a"] {
		t.Fatalf("同组应互通、跨组应隔离、自己应排除，实际 %v", got)
	}
}

func TestOrgAdminSeesAllGroups(t *testing.T) {
	self := dev("admin", 10, 1, model.VPNRoleOrgAdmin)
	all := []DeviceRef{self, dev("b", 11, 2, model.VPNRoleMember), dev("c", 12, 3, model.VPNRoleMember)}
	got := ids(VisiblePeers(self, all, nil))
	if !got["b"] || !got["c"] {
		t.Fatalf("机构管理员应穿透本机构所有组，实际 %v", got)
	}
}

func TestCrossOrgNeverVisible(t *testing.T) {
	self := dev("admin", 10, 1, model.VPNRoleOrgAdmin)
	other := dev("x", 11, 9, model.VPNRoleMember)
	other.OrgID = 2
	got := ids(VisiblePeers(self, []DeviceRef{self, other}, nil))
	if got["x"] {
		t.Fatalf("跨机构必须完全隔离")
	}
}

func TestInteropAdminsOnly(t *testing.T) {
	self := dev("a", 10, 1, model.VPNRoleGroupAdmin)
	peerAdmin := dev("b", 11, 2, model.VPNRoleGroupAdmin)
	peerMember := dev("c", 11, 3, model.VPNRoleMember)
	all := []DeviceRef{self, peerAdmin, peerMember}
	interops := []model.VPNInterop{{OrgID: 1, AGroupID: 10, BGroupID: 11, Scope: model.VPNInteropAdminsOnly, Enabled: true}}

	got := ids(VisiblePeers(self, all, interops))
	if !got["b"] || got["c"] {
		t.Fatalf("admins_only 应只放行两组管理员，实际 %v", got)
	}

	// 关闭后立即恢复隔离
	interops[0].Enabled = false
	got = ids(VisiblePeers(self, all, interops))
	if got["b"] {
		t.Fatalf("关闭互通后不应再可见")
	}
}

func TestInteropFull(t *testing.T) {
	self := dev("a", 10, 1, model.VPNRoleMember)
	peer := dev("b", 11, 2, model.VPNRoleMember)
	interops := []model.VPNInterop{{OrgID: 1, AGroupID: 11, BGroupID: 10, Scope: model.VPNInteropFull, Enabled: true}}

	got := ids(VisiblePeers(self, []DeviceRef{self, peer}, interops))
	if !got["b"] {
		t.Fatalf("full 模式下整组应互通（且不区分 A/B 方向）")
	}
}

func TestRevokedPeerInvisible(t *testing.T) {
	self := dev("a", 10, 1, model.VPNRoleMember)
	peer := dev("b", 10, 2, model.VPNRoleMember)
	peer.Status = model.VPNDeviceRevoked
	if ids(VisiblePeers(self, []DeviceRef{self, peer}, nil))["b"] {
		t.Fatalf("已吊销设备不应出现在对端列表")
	}
}

func TestNextAddressSkipsUsed(t *testing.T) {
	addr, err := NextAddress("10.12.3.0/24", map[string]bool{"10.12.3.1": true, "10.12.3.2": true})
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.12.3.3" {
		t.Fatalf("应跳过已用地址，拿到 10.12.3.3，实际 %s", addr)
	}
}

func TestNextAddressRejectsTinySubnet(t *testing.T) {
	if _, err := NextAddress("10.0.0.0/31", nil); err == nil {
		t.Fatalf("/31 不应有可用主机")
	}
	if _, err := NextAddress("fd00::/64", nil); err == nil {
		t.Fatalf("当前应拒绝 IPv6 网段")
	}
}

func TestValidateSubnet(t *testing.T) {
	for _, good := range []string{"10.0.0.0/24", "10.12.3.0/30", "172.16.0.0/16"} {
		if err := ValidateSubnet(good); err != nil {
			t.Fatalf("%s 应合法: %v", good, err)
		}
	}
	for _, bad := range []string{"10.0.0.1/24", "10.0.0.0/31", "fd00::/64", "not-a-subnet"} {
		if err := ValidateSubnet(bad); err == nil {
			t.Fatalf("%s 应被拒绝", bad)
		}
	}
}

func TestAllowedIPsOf(t *testing.T) {
	got := AllowedIPsOf("10.12.3.5")
	if len(got) != 1 || got[0] != "10.12.3.5/32" {
		t.Fatalf("应为单个 /32，实际 %v", got)
	}
}

func TestOrgAdminDeviceVisibleToAllInOrg(t *testing.T) {
	member := dev("m", 10, 1, model.VPNRoleMember)
	admin := dev("adm", 11, 2, model.VPNRoleOrgAdmin)
	other := dev("x", 11, 3, model.VPNRoleMember)

	got := ids(VisiblePeers(member, []DeviceRef{member, admin, other}, nil))
	if !got["adm"] {
		t.Fatalf("机构管理员设备必须对同机构成员可见，否则握手是单向的，实际 %v", got)
	}
	if got["x"] {
		t.Fatalf("同组之外的普通成员仍应隔离，实际 %v", got)
	}
}
