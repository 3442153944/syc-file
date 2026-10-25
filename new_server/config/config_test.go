package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

// writeTempConfig 在临时目录写一份最小 config.yaml，并把 SYC_CONFIG_DIR 指过去。
func writeTempConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("写临时配置失败: %v", err)
	}
	t.Setenv("SYC_CONFIG_DIR", dir)
}

// clearMeshEnv 清掉可能从外部环境带进来的组网环境变量，保证用例可重复。
func clearMeshEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"SYC_MODE",
		"SYC_VPN_SERVER_PUBKEY",
		"SYC_VPN_SERVER_ENDPOINT",
		"SYC_VPN_SERVER_INTERFACE",
		"SYC_VPN_SERVER_WG_PORT",
		"SYC_VPN_SERVER_LAN_ENDPOINTS",
	} {
		t.Setenv(env, "")
	}
}

// TestInitServerLANEndpoints 覆盖新增的 server_lan_endpoints 配置绑定：
// SYC_VPN_SERVER_LAN_ENDPOINTS 逗号分隔值要能解析成 []string 并去掉空格/空项/重复，
// 直接写在 config.yaml 里的列表也要生效。
func TestInitServerLANEndpoints(t *testing.T) {
	t.Run("环境变量逗号分隔并规范化", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		writeTempConfig(t, "server:\n  mode: dev\n")
		clearMeshEnv(t)
		t.Setenv("SYC_VPN_SERVER_LAN_ENDPOINTS", " 192.168.31.100:51821, 10.0.0.2:51821 ,192.168.31.100:51821")
		if err := Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		want := []string{"192.168.31.100:51821", "10.0.0.2:51821"}
		if got := Conf.Vpn.ServerLANEndpoints; !reflect.DeepEqual(got, want) {
			t.Fatalf("ServerLANEndpoints = %#v, want %#v", got, want)
		}
		if Conf.Vpn.ServerWGPort != 51820 {
			t.Fatalf("默认 ServerWGPort = %d, want 51820", Conf.Vpn.ServerWGPort)
		}
	})

	t.Run("config.yaml 列表绑定", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		writeTempConfig(t, "server:\n  mode: dev\nvpn:\n  server_lan_endpoints:\n    - 10.12.3.1:51820\n    - 10.12.3.1:51820\n    - 10.12.3.2:51820\n")
		clearMeshEnv(t)
		if err := Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		want := []string{"10.12.3.1:51820", "10.12.3.2:51820"}
		if got := Conf.Vpn.ServerLANEndpoints; !reflect.DeepEqual(got, want) {
			t.Fatalf("ServerLANEndpoints = %#v, want %#v", got, want)
		}
	})
}
