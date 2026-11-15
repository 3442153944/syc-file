package vpn

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"syc-file/config"
)

// testGinContext 构造一个最小的 gin 上下文，用来测依赖请求头/主机名的函数。
func testGinContext(t *testing.T, target, forwardedHost string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", target, nil)
	if forwardedHost != "" {
		c.Request.Header.Set("X-Forwarded-Host", forwardedHost)
	}
	return c
}

func TestServerEndpointFor(t *testing.T) {
	cases := []struct {
		name          string
		target        string
		forwardedHost string
		cfg           config.VpnConfig
		want          string
	}{
		{
			name:   "显式配置优先且去空白",
			target: "http://192.168.31.100:8992/v1/vpn/peers",
			cfg:    config.VpnConfig{ServerEndpoint: " 8.137.77.99:12000 "},
			want:   "8.137.77.99:12000",
		},
		{
			name:   "默认用请求 Host 加默认 wg 端口",
			target: "http://192.168.31.100:8992/v1/vpn/peers",
			want:   "192.168.31.100:51820",
		},
		{
			name:   "自定义 wg 端口",
			target: "http://192.168.31.100:8992/v1/vpn/peers",
			cfg:    config.VpnConfig{ServerWGPort: 52000},
			want:   "192.168.31.100:52000",
		},
		{
			name:          "X-Forwarded-Host 优先且只取第一个",
			target:        "http://127.0.0.1:8992/v1/vpn/peers",
			forwardedHost: "ali.example:443, proxy.local:8080",
			want:          "ali.example:51820",
		},
		{
			name:          "X-Forwarded-Host 不带端口",
			target:        "http://127.0.0.1:8992/v1/vpn/peers",
			forwardedHost: "192.168.31.100",
			want:          "192.168.31.100:51820",
		},
		{
			name:   "IPv6 Host",
			target: "http://[fd00::1]:8992/v1/vpn/peers",
			want:   "[fd00::1]:51820",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serverEndpointFor(testGinContext(t, tc.target, tc.forwardedHost), tc.cfg); got != tc.want {
				t.Fatalf("serverEndpointFor = %q, want %q", got, tc.want)
			}
		})
	}

	// Host 为空时返回空串，由调用方自行处理（不能拼出 ":51820" 这种端点）。
	c := testGinContext(t, "http://192.168.31.100:8992/v1/vpn/peers", "")
	c.Request.Host = ""
	if got := serverEndpointFor(c, config.VpnConfig{}); got != "" {
		t.Fatalf("空 Host 应返回空端点, got %q", got)
	}
}

func TestServerPeerCandidatesFilterAndDedup(t *testing.T) {
	old := config.Conf.Vpn
	t.Cleanup(func() { config.Conf.Vpn = old })
	config.Conf.Vpn = config.VpnConfig{
		ServerPubKey:  "server-pubkey",
		ServerAddress: "172.1.0.254",
		ServerWGPort:  51820,
		// 模拟环境变量逗号分隔带空格/空项/重复/回环的脏配置。
		ServerLANEndpoints: []string{" 192.168.31.100:51820 ", "", "127.0.0.1:51820", "192.168.31.100:51820"},
	}
	peer := ServerPeer(testGinContext(t, "http://192.168.31.100:8992/v1/vpn/peers", ""))
	if peer == nil {
		t.Fatal("配置了 ServerPubKey 时不应返回 nil")
	}
	candidates, ok := peer["candidates"].([]string)
	if !ok || len(candidates) != 1 || candidates[0] != "192.168.31.100:51820" {
		t.Fatalf("server candidates = %#v", peer["candidates"])
	}
	if got := peer["endpoint"]; got != "192.168.31.100:51820" {
		t.Fatalf("server endpoint = %v", got)
	}
	if got, ok := peer["allowed_ips"].([]string); !ok || len(got) != 1 || got[0] != "172.1.0.254/32" {
		t.Fatalf("server allowed_ips = %#v", peer["allowed_ips"])
	}
	if got := peer["role"]; got != "server" {
		t.Fatalf("server role = %v", got)
	}
}

func TestServerPeerWithoutPubKey(t *testing.T) {
	old := config.Conf.Vpn
	t.Cleanup(func() { config.Conf.Vpn = old })
	config.Conf.Vpn = config.VpnConfig{ServerPubKey: "  "}
	if peer := ServerPeer(testGinContext(t, "http://192.168.31.100:8992/v1/vpn/peers", "")); peer != nil {
		t.Fatalf("未配置 ServerPubKey 时应返回 nil, got %#v", peer)
	}
}
