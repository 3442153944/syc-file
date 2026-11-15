package vpn

import (
	"strconv"
	"testing"
)

func TestLANEndpointsRejectProxyAndNonLANAddresses(t *testing.T) {
	for _, endpoint := range []string{"127.0.0.1:51821", "[::1]:51821", "0.0.0.0:51821", "203.0.113.1:51821", "192.168.31.2:0", "192.168.31.2", "proxy.example:51821"} {
		if _, err := validateLANEndpoints([]string{endpoint}); err == nil {
			t.Fatalf("accepted %q", endpoint)
		}
	}
	// 允许前后空白与 IPv6 ULA，重复项去重后只留一个。
	got, err := validateLANEndpoints([]string{" 192.168.31.2:51821 ", "192.168.31.2:51821", "[fd00::1]:51821"})
	if err != nil || len(got) != 2 || got[0] != "192.168.31.2:51821" || got[1] != "[fd00::1]:51821" {
		t.Fatalf("dedup = %v, %v", got, err)
	}
}

func TestLANEndpointsCapAtEight(t *testing.T) {
	nine := make([]string, 0, maxLANEndpoints+1)
	for i := 0; i <= maxLANEndpoints; i++ {
		nine = append(nine, "192.168.31."+strconv.Itoa(i+1)+":51821")
	}
	if _, err := validateLANEndpoints(nine); err == nil {
		t.Fatalf("%d 个 LAN 端点应被拒绝", len(nine))
	}
	if _, err := validateLANEndpoints(nine[:maxLANEndpoints]); err != nil {
		t.Fatalf("恰好 %d 个应通过: %v", maxLANEndpoints, err)
	}
}

func TestCandidatesPreferLANAndDiscardRelayLoopback(t *testing.T) {
	got := endpointCandidates(`["192.168.31.2:51821"]`, "127.0.0.1:12345", "203.0.113.1:51821")
	if len(got) != 2 || got[0] != "192.168.31.2:51821" || got[1] != "203.0.113.1:51821" {
		t.Fatalf("candidates = %v", got)
	}
}

func TestCandidatesIgnoreCorruptLANJSONAndDedup(t *testing.T) {
	if got := endpointCandidates("not-json", "", ""); len(got) != 0 {
		t.Fatalf("损坏的 LAN JSON 应按空候选处理, got %v", got)
	}
	// observed 与 reported 是同一个端点时只保留一个。
	got := endpointCandidates("[]", "203.0.113.1:51821", "203.0.113.1:51821")
	if len(got) != 1 || got[0] != "203.0.113.1:51821" {
		t.Fatalf("dedup = %v", got)
	}
	// 未指定/回环/非法端点都不下发。
	if got := endpointCandidates("[]", "0.0.0.0:51821", "[::1]:51821"); len(got) != 0 {
		t.Fatalf("非法端点应被丢弃, got %v", got)
	}
}

func TestServerCandidatesFilterAndDedup(t *testing.T) {
	got := serverCandidates([]string{"192.168.31.100:51820", "", "127.0.0.1:51820", "192.168.31.100:51820"}, "192.168.31.100:51820")
	if len(got) != 1 || got[0] != "192.168.31.100:51820" {
		t.Fatalf("server candidates = %v", got)
	}
	if got := serverCandidates(nil, ""); len(got) != 0 {
		t.Fatalf("空端点应被丢弃, got %v", got)
	}
	got = serverCandidates([]string{"192.168.31.100:51820"}, "[fd00::1]:51820")
	if len(got) != 2 || got[1] != "[fd00::1]:51820" {
		t.Fatalf("LAN 候选应排在前, got %v", got)
	}
}
