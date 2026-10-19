package vpn

import "testing"

func TestAllocateSubnetFromDefaultPool(t *testing.T) {
	got, err := AllocateSubnet("172.1.0.0/16", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "172.1.0.0/24" {
		t.Fatalf("空池的第一个 /24 应为 172.1.0.0/24，实际 %s", got)
	}

	got, err = AllocateSubnet("172.1.0.0/16", []string{"172.1.0.0/24", "172.1.1.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "172.1.2.0/24" {
		t.Fatalf("跳过已用网段后应拿 172.1.2.0/24，实际 %s", got)
	}
}

func TestAllocateSubnetRejectsTinyPool(t *testing.T) {
	if _, err := AllocateSubnet("172.1.0.0/25", nil); err == nil {
		t.Fatal("比 /24 更小的池应被拒绝")
	}
	if _, err := AllocateSubnet("not-a-prefix", nil); err == nil {
		t.Fatal("非法池应被拒绝")
	}
	if _, err := AllocateSubnet("fd00::/48", nil); err == nil {
		t.Fatal("IPv6 池应被拒绝")
	}
}

func TestSubnetInPool(t *testing.T) {
	cases := []struct {
		subnet string
		pool   string
		want   bool
	}{
		{"172.1.0.0/24", "172.1.0.0/16", true},
		{"172.1.255.0/24", "172.1.0.0/16", true},
		{"172.2.0.0/24", "172.1.0.0/16", false},
		{"172.0.0.0/24", "172.1.0.0/16", false},
		{"172.1.0.0/15", "172.1.0.0/16", false}, // 比池还宽
		{"fd00::/64", "172.1.0.0/16", false},
	}
	for _, c := range cases {
		if got := SubnetInPool(c.subnet, c.pool); got != c.want {
			t.Fatalf("SubnetInPool(%s, %s) = %v，期望 %v", c.subnet, c.pool, got, c.want)
		}
	}
}

func TestAllocateSubnetMatchesNextAddress(t *testing.T) {
	subnet, err := AllocateSubnet("172.1.0.0/16", nil)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := NextAddress(subnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "172.1.0.1" {
		t.Fatalf("新建组的第一个设备地址应为 172.1.0.1，实际 %s", addr)
	}
}
