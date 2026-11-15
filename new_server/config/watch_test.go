package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fastWatch 把轮询/防抖调快，测试不用等默认的 1s 轮询。
// 必须在 StartWatcher 之前调用并恢复（StartWatcher 只在启动时读一次）。
func fastWatch(t *testing.T) {
	t.Helper()
	oldPoll, oldDebounce := watchPollInterval, watchDebounce
	watchPollInterval = 20 * time.Millisecond
	watchDebounce = 5 * time.Millisecond
	t.Cleanup(func() {
		watchPollInterval = oldPoll
		watchDebounce = oldDebounce
	})
}

// hookRecorder 记录 config 包通过钩子写出的日志 / 设置的级别。
type hookRecorder struct {
	mu     sync.Mutex
	logs   []string
	levels []string
}

func (r *hookRecorder) register(t *testing.T) {
	t.Helper()
	RegisterLoggerHooks(func(level string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.levels = append(r.levels, level)
		return nil
	}, func(format string, args ...any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, fmt.Sprintf(format, args...))
	})
	t.Cleanup(func() { RegisterLoggerHooks(nil, nil) })
}

func (r *hookRecorder) messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.logs...)
}

func (r *hookRecorder) lastMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.logs) == 0 {
		return ""
	}
	return r.logs[len(r.logs)-1]
}

// waitFor 轮询等待条件成立（默认最多 2s），避免写死 sleep 造成 flaky。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// TestStartWatcherAppliesHotFields 热字段变化 → 发布新快照 + 热改日志级别；冷字段只记日志。
func TestStartWatcherAppliesHotFields(t *testing.T) {
	clearMeshEnv(t)
	fastWatch(t)
	live.Store(nil)
	t.Cleanup(func() { live.Store(nil) })

	_, path := writeEditorFixture(t, editorFixture)
	baseCfg, err := LoadText(editorFixture)
	if err != nil {
		t.Fatalf("LoadText: %v", err)
	}
	PublishLive(Clone(baseCfg))

	rec := &hookRecorder{}
	rec.register(t)

	stop, err := StartWatcher(context.Background(), path)
	if err != nil {
		t.Fatalf("StartWatcher: %v", err)
	}
	defer stop()

	// 1) 改热字段：log.level + 新增 monitor 段（monitor.* 也是热字段）
	changed := strings.Replace(editorFixture, "level: info", "level: debug", 1) +
		"\nmonitor:\n  sys_detail_interval_seconds: 5\n  process_top_n: 30\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	waitFor(t, "热字段生效", func() bool {
		return Live().Log.Level == "debug" && Live().Monitor.ProcessTopN == 30
	})
	// 日志级别钩子被调用且是 debug
	waitFor(t, "热改日志级别", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, lv := range rec.levels {
			if lv == "debug" {
				return true
			}
		}
		return false
	})

	// 2) 只改冷字段：文件落盘，但 Live 不动，日志提示需重启
	coldChanged := strings.Replace(changed, "port: 8991", "port: 9000", 1)
	if err := os.WriteFile(path, []byte(coldChanged), 0o600); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	waitFor(t, "冷字段提示", func() bool {
		return strings.Contains(rec.lastMessage(), "server.port")
	})
	if Live().Server.Port != 8991 {
		t.Fatalf("冷字段 server.port 不应进 Live：%d", Live().Server.Port)
	}
	// 热字段保持上一轮的值
	if Live().Log.Level != "debug" {
		t.Fatalf("热字段被冷字段更新覆盖: %q", Live().Log.Level)
	}

	// 3) 写入非法 YAML：只记错误日志，不发布、不影响正在运行的配置
	if err := os.WriteFile(path, []byte("db: [\n"), 0o600); err != nil {
		t.Fatalf("写非法配置失败: %v", err)
	}
	waitFor(t, "非法配置报错", func() bool {
		return strings.Contains(rec.lastMessage(), "热重载失败")
	})
	if Live().Log.Level != "debug" || Live().Server.Port != 8991 {
		t.Fatalf("非法配置不应改变 Live: %+v", Live())
	}

	// 4) 恢复正常配置：日志级别变 warn → 再次热生效
	recovered := strings.Replace(editorFixture, "level: info", "level: warn", 1)
	if err := os.WriteFile(path, []byte(recovered), 0o600); err != nil {
		t.Fatalf("写恢复配置失败: %v", err)
	}
	waitFor(t, "恢复后热生效", func() bool { return Live().Log.Level == "warn" })
}

// TestStartWatcherRejectsMissingFile 文件不存在时启动失败（启动阶段配置就是必须的）。
func TestStartWatcherRejectsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-exist.yaml")
	stop, err := StartWatcher(context.Background(), path)
	if err == nil {
		stop()
		t.Fatal("监听不存在的文件应返回错误")
	}
}

// TestStartWatcherStopIsIdempotent stop 可以重复调用，ctx 取消也会退出。
func TestStartWatcherStopIsIdempotent(t *testing.T) {
	clearMeshEnv(t)
	fastWatch(t)
	_, path := writeEditorFixture(t, editorFixture)

	ctx, cancel := context.WithCancel(context.Background())
	stop, err := StartWatcher(ctx, path)
	if err != nil {
		t.Fatalf("StartWatcher: %v", err)
	}
	cancel()
	stop()
	stop() // 不应 panic / 不应重复 wait

	// 用 ctx 取消的方式启动再验证 stop 仍然安全
	stop2, err := StartWatcher(context.Background(), path)
	if err != nil {
		t.Fatalf("StartWatcher: %v", err)
	}
	stop2()
	stop2()
}
