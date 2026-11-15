package config

import (
	"reflect"
	"testing"
)

// TestHotPathsResolvable 热字段清单里的每个路径都必须在 Config 里真实存在（防手滑写错）。
func TestHotPathsResolvable(t *testing.T) {
	paths := HotPaths()
	if len(paths) == 0 {
		t.Fatal("热字段清单为空")
	}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("热字段重复: %s", p)
		}
		seen[p] = true
		if configFieldType(p) == nil {
			t.Fatalf("热字段路径在 Config 中不存在: %s", p)
		}
		if !IsHot(p) {
			t.Fatalf("IsHot(%q) 应为 true", p)
		}
	}

	// 冷字段必须不在热清单里
	for _, cold := range []string{"server.port", "server.mode", "db.host", "redis.host", "log.path", "file.storage.base_path", "share.temp_path"} {
		if IsHot(cold) {
			t.Fatalf("冷字段被误标为热字段: %s", cold)
		}
	}
	if IsHot("no.such.path") {
		t.Fatal("不存在的路径不应是热字段")
	}

	// HotPaths 返回副本，外部改动不能污染内部清单
	paths[0] = "tampered"
	if HotPaths()[0] == "tampered" {
		t.Fatal("HotPaths 应返回副本")
	}
}

// TestApplyHotIntoOnlyMovesHotFields 只搬热字段；冷字段保持旧值；slice 深拷贝。
func TestApplyHotIntoOnlyMovesHotFields(t *testing.T) {
	dst := &Config{
		Log:        LogConfig{Level: "info", Path: "./log"},
		Server:     ServerConfig{Port: 8991, Name: "old-name"},
		Whitelist:  []string{"/v1/ping"},
		File:       FileConfig{AllowedPaths: []string{"D:"}, Upload: UploadCfg{MaxFileSize: 1024, MaxFilenameLength: 100}},
		Auth:       AuthConfig{TokenExpire: 7, RefreshExpire: 1},
		QuickShare: QuickShareConfig{MaxCapacityBytes: 100},
	}
	next := Clone(dst)
	next.Log.Level = "debug"
	next.Whitelist = []string{"/v1/ping", "/v1/health"}
	next.File.Upload.MaxFileSize = 2048
	next.File.Upload.MaxFilenameLength = 200
	next.Auth.TokenExpire = 30
	next.Auth.RefreshExpire = 2
	next.QuickShare.MaxCapacityBytes = 500
	next.QuickShare.MemoryThresholdBytes = 64
	next.QuickShare.DefaultExpireMinutes = 90
	next.Share.MaxExpireMinutes = 60
	next.User.MaxSize = 4096
	next.Monitor.SysDetailIntervalSeconds = 5
	next.Monitor.ProcessTopN = 10
	next.File.AllowedPaths = []string{"E:"}
	next.File.Upload.AllowedExtensions = []string{".png"}
	next.File.Upload.ForbiddenExtensions = []string{".exe"}
	// 冷字段变化不应被搬过去
	next.Server.Port = 9000
	next.Server.Name = "new-name"
	next.Log.Path = "/var/log"

	applied := ApplyHotInto(dst, next)

	want := []string{
		"log.level",
		"monitor.sys_detail_interval_seconds",
		"monitor.process_top_n",
		"file.allowed_paths",
		"file.upload.max_file_size",
		"file.upload.max_filename_length",
		"file.upload.allowed_extensions",
		"file.upload.forbidden_extensions",
		"whitelist",
		"auth.token_expire",
		"auth.refresh_expire",
		"share.max_expire_minutes",
		"quick_share.default_expire_minutes",
		"quick_share.max_capacity_bytes",
		"quick_share.memory_threshold_bytes",
		"user.max_size",
	}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("ApplyHotInto 返回的字段不对:\n got %v\nwant %v", applied, want)
	}

	if dst.Log.Level != "debug" || dst.Auth.TokenExpire != 30 || dst.Share.MaxExpireMinutes != 60 {
		t.Fatalf("热字段没有搬过去: %#v", dst)
	}
	if dst.Server.Port != 8991 || dst.Server.Name != "old-name" || dst.Log.Path != "./log" {
		t.Fatalf("冷字段不应被修改: %#v", dst.Server)
	}

	// 深拷贝：改 next 的 slice 不能影响 dst
	next.Whitelist[0] = "/v1/tampered"
	next.File.AllowedPaths[0] = "Z:"
	if dst.Whitelist[0] != "/v1/ping" || dst.File.AllowedPaths[0] != "E:" {
		t.Fatal("热字段 slice 没有深拷贝")
	}

	// 没有变化时返回空
	if got := ApplyHotInto(dst, Clone(dst)); len(got) != 0 {
		t.Fatalf("无变化时不应返回字段: %v", got)
	}
	// nil 入参不 panic
	if got := ApplyHotInto(nil, next); got != nil {
		t.Fatal("ApplyHotInto(nil, x) 应返回 nil")
	}
	if got := ApplyHotInto(dst, nil); got != nil {
		t.Fatal("ApplyHotInto(x, nil) 应返回 nil")
	}
}

// TestClassifyChanges 热/冷字段分类正确，且结果有序。
func TestClassifyChanges(t *testing.T) {
	base := &Config{
		DB:        DBConfig{Host: "127.0.0.1", Port: 3306},
		Log:       LogConfig{Level: "info", Path: "./log"},
		Auth:      AuthConfig{TokenExpire: 7, RefreshExpire: 1},
		Server:    ServerConfig{Port: 8991, Mode: ModeDev},
		Whitelist: []string{"/v1/ping"},
	}
	next := Clone(base)
	next.Log.Level = "debug"                            // 热
	next.Whitelist = []string{"/v1/ping", "/v1/health"} // 热
	next.Auth.TokenExpire = 14                          // 热
	next.Server.Port = 9000                             // 冷
	next.DB.Host = "10.0.0.1"                           // 冷
	next.Log.Path = "/var/log"                          // 冷

	reloaded, needRestart := ClassifyChanges(base, next)

	wantReloaded := []string{"auth.token_expire", "log.level", "whitelist"}
	if !reflect.DeepEqual(reloaded, wantReloaded) {
		t.Fatalf("reloaded = %v, want %v", reloaded, wantReloaded)
	}
	wantRestart := []string{"db.host", "log.path", "server.port"}
	if !reflect.DeepEqual(needRestart, wantRestart) {
		t.Fatalf("needRestart = %v, want %v", needRestart, wantRestart)
	}

	// 完全一致时两组都为空
	if r, n := ClassifyChanges(base, Clone(base)); len(r) != 0 || len(n) != 0 {
		t.Fatalf("无变化时不应有输出: reloaded=%v needRestart=%v", r, n)
	}
	// newC 为 nil 直接返回空
	if r, n := ClassifyChanges(base, nil); r != nil || n != nil {
		t.Fatal("newC 为 nil 应返回 nil")
	}
	// oldC 为 nil：按全零配置对比，不能 panic
	if r, n := ClassifyChanges(nil, base); len(r) == 0 && len(n) == 0 {
		t.Fatal("oldC 为 nil 时应至少识别出变化字段")
	}
}
