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

// clearMeshEnv 清掉可能从外部环境带进来的运行模式变量，保证用例可重复。
// 组网配置只从 config.yaml 读取，不依赖环境变量。
func clearMeshEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SYC_MODE", "")
}

// TestInitServerLANEndpoints 覆盖 config.yaml 里 server_lan_endpoints 列表绑定：
// 列表要规范化去重；组网配置不再提供环境变量入口。
func TestInitServerLANEndpoints(t *testing.T) {
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
	if Conf.Vpn.ServerWGPort != 51820 {
		t.Fatalf("默认 ServerWGPort = %d, want 51820", Conf.Vpn.ServerWGPort)
	}
}
