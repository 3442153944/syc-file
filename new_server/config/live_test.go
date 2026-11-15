package config

import (
	"sync"
	"testing"

	"syc-file/internal/supervisor"
)

// resetLive 把全局 Live 快照恢复成“未发布”状态（Live() 回落到 Conf），避免测试相互影响。
func resetLive(t *testing.T) {
	t.Helper()
	live.Store(nil)
	t.Cleanup(func() { live.Store(nil) })
}

// TestLiveFallbackToConf 未发布过快照时 Live() 必须返回启动期加载的 Conf。
func TestLiveFallbackToConf(t *testing.T) {
	resetLive(t)
	if got := Live(); got != Conf {
		t.Fatal("未 PublishLive 时 Live() 应返回 Conf")
	}

	published := &Config{Log: LogConfig{Level: "debug"}}
	PublishLive(published)
	if got := Live(); got != published {
		t.Fatal("PublishLive 后 Live() 应返回新快照")
	}

	// nil 不能把当前配置清空（否则读侧会 panic 或退回 Conf 拿到旧值）
	PublishLive(nil)
	if got := Live(); got != published {
		t.Fatal("PublishLive(nil) 不应改变当前快照")
	}
}

// TestCloneDeepCopiesSlices 克隆必须逐层复制 slice，不能和源对象共享底层数组。
func TestCloneDeepCopiesSlices(t *testing.T) {
	src := &Config{
		Whitelist: []string{"/v1/ping"},
		Vpn: VpnConfig{
			ServerLANEndpoints: []string{"10.0.0.1:51820"},
		},
		File: FileConfig{
			AllowedPaths: []string{"D:"},
			Upload: UploadCfg{
				AllowedExtensions:   []string{".png"},
				ForbiddenExtensions: []string{".exe"},
			},
		},
		User: UserCfg{AllowedExtensions: []string{".jpg"}},
		Supervisor: supervisor.Config{
			Processes: []supervisor.ProcessConfig{
				{Name: "frpc", Args: []string{"-c", "frpc.toml"}},
			},
		},
	}

	clone := Clone(src)
	if clone == nil || clone == src {
		t.Fatal("Clone 应返回新对象")
	}

	// 改克隆不影响源
	clone.Whitelist[0] = "/v1/health"
	clone.Vpn.ServerLANEndpoints[0] = "10.0.0.2:51820"
	clone.File.AllowedPaths[0] = "E:"
	clone.File.Upload.AllowedExtensions[0] = ".gif"
	clone.File.Upload.ForbiddenExtensions[0] = ".bat"
	clone.User.AllowedExtensions[0] = ".webp"
	clone.Supervisor.Processes[0].Args[0] = "-d"
	clone.Supervisor.Processes[0].Name = "ddns-go"

	if src.Whitelist[0] != "/v1/ping" ||
		src.Vpn.ServerLANEndpoints[0] != "10.0.0.1:51820" ||
		src.File.AllowedPaths[0] != "D:" ||
		src.File.Upload.AllowedExtensions[0] != ".png" ||
		src.File.Upload.ForbiddenExtensions[0] != ".exe" ||
		src.User.AllowedExtensions[0] != ".jpg" ||
		src.Supervisor.Processes[0].Args[0] != "-c" ||
		src.Supervisor.Processes[0].Name != "frpc" {
		t.Fatalf("Clone 没有深拷贝，源对象被联动修改: %#v", src)
	}

	// 改源不影响克隆（反向确认不共享底层数组）
	src.Whitelist[0] = "/v1/public"
	if clone.Whitelist[0] != "/v1/health" {
		t.Fatal("Clone 与源共享了底层数组")
	}

	if Clone(nil) != nil {
		t.Fatal("Clone(nil) 应返回 nil")
	}
	empty := Clone(&Config{})
	if empty == nil {
		t.Fatal("Clone(&Config{}) 不应返回 nil")
	}
}

// TestLiveConcurrentReadWrite 并发读快照 + 并发发布新快照，配合 -race 验证无数据竞争。
//
// 读侧只读已发布快照（约定发布后不再修改），写侧每次 Clone 出独立副本再发布，
// 这是热重载的标准用法。
func TestLiveConcurrentReadWrite(t *testing.T) {
	resetLive(t)
	PublishLive(Clone(Conf))

	var readers, writers sync.WaitGroup
	stop := make(chan struct{})

	// 读侧：模拟请求处理 goroutine 反复读 Live() 的各个热字段。
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cfg := Live()
				_ = cfg.Log.Level
				_ = cfg.File.Upload.MaxFileSize
				_ = cfg.Auth.TokenExpire
				if n := len(cfg.Whitelist); n > 0 {
					_ = cfg.Whitelist[n-1]
				}
				if n := len(cfg.File.AllowedPaths); n > 0 {
					_ = cfg.File.AllowedPaths[n-1]
				}
			}
		}()
	}

	// 写侧：模拟 watcher 反复“克隆 → 改热字段 → 发布”。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 500; i++ {
			next := Clone(Live())
			next.Log.Level = "debug"
			next.Auth.TokenExpire = i%30 + 1
			next.Whitelist = []string{"/v1/ping", "/v1/health"}
			PublishLive(next)
		}
	}()

	writers.Wait()
	close(stop)
	readers.Wait()

	last := Live()
	if last.Log.Level != "debug" || last.Auth.TokenExpire != (500-1)%30+1 {
		t.Fatalf("并发发布后快照内容不对: level=%q token_expire=%d", last.Log.Level, last.Auth.TokenExpire)
	}
}

// TestLiveSnapshotsAreImmutable 发布后再 Clone 修改副本，已发布的旧快照不应变化。
func TestLiveSnapshotsAreImmutable(t *testing.T) {
	resetLive(t)
	base := &Config{Log: LogConfig{Level: "info"}}
	PublishLive(base)

	next := Clone(Live())
	next.Log.Level = "error"
	PublishLive(next)

	if Live().Log.Level != "error" {
		t.Fatal("新快照未生效")
	}
	if base.Log.Level != "info" {
		t.Fatal("旧快照被修改（应 clone 后再改）")
	}
}
