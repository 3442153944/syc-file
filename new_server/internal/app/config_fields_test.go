package app

import (
	"strings"
	"testing"

	"syc-file/config"
	"syc-file/internal/desktopapi"
)

// TestCollectConfigFields 校验反射出来的字段表：分组/类型/热标记/掩码/列表拼接。
func TestCollectConfigFields(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.Port = 8991
	cfg.Server.Mode = "dev"
	cfg.Log.Level = "info"
	cfg.Auth.Secret = "top-secret"
	cfg.File.AllowedPaths = []string{`D:\data`, "E:\\"}

	byPath := make(map[string]desktopapi.ConfigField)
	for _, field := range collectConfigFields(cfg) {
		if _, dup := byPath[field.Path]; dup {
			t.Fatalf("字段路径重复: %s", field.Path)
		}
		byPath[field.Path] = field
	}

	port, ok := byPath["server.port"]
	if !ok || port.Kind != desktopapi.KindInt || port.Value != "8991" || port.Group != "服务" {
		t.Fatalf("server.port 字段不对: %+v", port)
	}
	if port.HotReload {
		t.Fatal("server.port 不应是热字段")
	}

	mode, ok := byPath["server.mode"]
	if !ok || mode.Kind != desktopapi.KindSelect || len(mode.Options) != 2 {
		t.Fatalf("server.mode 字段不对: %+v", mode)
	}

	level, ok := byPath["log.level"]
	if !ok || !level.HotReload {
		t.Fatalf("log.level 应该是热字段: %+v", level)
	}

	secret, ok := byPath["auth.secret"]
	if !ok || secret.Kind != desktopapi.KindSecret || secret.Value != "********" {
		t.Fatalf("auth.secret 应该掩码: %+v", secret)
	}

	paths, ok := byPath["file.allowed_paths"]
	if !ok || paths.Kind != desktopapi.KindList {
		t.Fatalf("file.allowed_paths 字段不对: %+v", paths)
	}
	if !strings.Contains(paths.Value, "\n") {
		t.Fatalf("列表字段应以换行连接: %q", paths.Value)
	}

	// supervisor.processes 是 []struct，不应该出现在表单里（无法安全地文本化）。
	if _, exists := byPath["supervisor.processes"]; exists {
		t.Fatal("supervisor.processes 不应出现在表单字段里")
	}
}
