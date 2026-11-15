package config

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 配置文件监听参数。
//
// 用 1s mtime 轮询而不是 fsnotify：
//   - 不新增依赖（fsnotify 目前只是 viper 的间接依赖）；
//   - 本包和 GUI 保存都是“写临时文件 + rename 替换”，轮询对 rename / 删除重建都稳，
//     而 Windows 下的文件监听对 rename 事件并不可靠。
//
// 变量形式是为了测试能调快；StartWatcher 在启动时各读一次，之后不再访问。
var (
	watchPollInterval = time.Second
	watchDebounce     = 300 * time.Millisecond
)

// loggerHooks 由 pkg/logger 注入。
//
// config 不能直接 import pkg/logger：logger 已经 import config，再加一条边就成导入环。
// 所以由 logger 反向注册钩子，watcher 通过钩子完成“热改日志级别”和“写错误日志”。
type loggerHooks struct {
	// setLevel 把 log.level 的新值应用到 zap（logger.SetLevel）。
	setLevel func(level string) error
	// printf 写一条日志（logger 侧会走 zap；未注册时退回标准库 log）。
	printf func(format string, args ...any)
}

var registeredLoggerHooks atomic.Pointer[loggerHooks]

// RegisterLoggerHooks 注册日志钩子，由 pkg/logger 在 init 阶段调用；重复调用以最后一次为准。
func RegisterLoggerHooks(setLevel func(level string) error, printf func(format string, args ...any)) {
	registeredLoggerHooks.Store(&loggerHooks{setLevel: setLevel, printf: printf})
}

// watchLogf 写一条监听器日志：优先走注入的 zap 日志（能落盘到 server.log），否则退回标准库 log。
func watchLogf(format string, args ...any) {
	if h := registeredLoggerHooks.Load(); h != nil && h.printf != nil {
		h.printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// applyLogLevel 把新的日志级别应用到 zap。配置里级别留空（未配置）时保持当前级别不动。
func applyLogLevel(level string) {
	level = strings.TrimSpace(level)
	if level == "" {
		return
	}
	h := registeredLoggerHooks.Load()
	if h == nil || h.setLevel == nil {
		return
	}
	if err := h.setLevel(level); err != nil {
		watchLogf("热重载日志级别失败（继续使用当前级别）: %v", err)
	}
}

// watcher 一个配置文件监听器。
type watcher struct {
	path     string
	interval time.Duration
	debounce time.Duration

	// 最近一次“已处理”的文件签名。hash 用来区分“真改了内容”和“只是被 touch 了一下”。
	lastMod  time.Time
	lastSize int64
	lastHash [sha256.Size]byte
}

// StartWatcher 监听配置文件变化，把其中的热字段应用到 Live 快照。
//
// 变化流程：读到稳定内容 → LoadText 解析 + Validate → 失败只记日志、不影响正在运行的配置 →
// 成功则 Clone(Live()) + ApplyHotInto + PublishLive，并同步热改日志级别。
// 冷字段只记日志（“需重启后生效”），不进 Live —— 进程还在按旧值跑，改 Live 只会让
// GUI 看到的“当前配置”和实际行为不一致。
//
// ctx 取消或调用返回的 stop 后监听停止；stop 幂等，会等待后台 goroutine 退出。
func StartWatcher(ctx context.Context, path string) (stop func(), err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("监听配置文件失败: %w", err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("监听配置文件失败: %s 是目录", abs)
	}

	w := &watcher{
		path:     abs,
		interval: watchPollInterval,
		debounce: watchDebounce,
		lastMod:  st.ModTime(),
		lastSize: st.Size(),
	}
	// 记下启动时的内容指纹：之后只有内容真的变了才重新加载（避免 just touched 也重载）。
	if data, readErr := os.ReadFile(abs); readErr == nil {
		w.lastHash = sha256.Sum256(data)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.run(watchCtx)
	}()

	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			wg.Wait()
		})
	}
	return stop, nil
}

// run 轮询主循环。
func (w *watcher) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !w.scanOnce(ctx) {
				return
			}
		}
	}
}

// scanOnce 做一次变化检测 + 重载；返回 false 表示 ctx 已取消。
func (w *watcher) scanOnce(ctx context.Context) bool {
	st, err := os.Stat(w.path)
	if err != nil {
		// 原子替换的空档、文件被临时删掉都会走到这里：不是错误，等下一轮。
		return true
	}
	if st.ModTime().Equal(w.lastMod) && st.Size() == w.lastSize {
		return true
	}

	// 防抖：编辑器保存可能分多步写，等文件稳定后再读，避免读到写了一半的内容。
	// 本包自己的保存是 rename 原子替换，通常第一次轮询就能读到完整文件。
	if !sleepWithContext(ctx, w.debounce) {
		return false
	}
	st2, err := os.Stat(w.path)
	if err != nil {
		return true
	}
	if !st2.ModTime().Equal(st.ModTime()) || st2.Size() != st.Size() {
		return true // 还在写，下一轮再看
	}

	data, err := os.ReadFile(w.path)
	if err != nil {
		watchLogf("读取配置文件失败，稍后重试: %v", err)
		return true
	}
	sum := sha256.Sum256(data)
	w.lastMod, w.lastSize = st2.ModTime(), st2.Size()
	if sum == w.lastHash {
		return true // 只是 mtime 变了，内容没变
	}
	w.lastHash = sum
	w.reload(data)
	return true
}

// reload 解析并应用新内容；失败只记日志、不发布，继续用旧配置。
func (w *watcher) reload(data []byte) {
	nextCfg, err := LoadText(string(data))
	if err != nil {
		watchLogf("配置文件热重载失败（未生效，继续使用旧配置）: %v", err)
		return
	}
	if err := Validate(nextCfg); err != nil {
		watchLogf("配置文件热重载失败（校验未通过，未生效）: %v", err)
		return
	}

	oldCfg := Live()
	reloaded, needRestart := ClassifyChanges(oldCfg, nextCfg)
	if len(reloaded) == 0 {
		if len(needRestart) > 0 {
			watchLogf("配置文件已更新：热字段无变化，%d 个冷字段需重启后生效: %s",
				len(needRestart), strings.Join(needRestart, ", "))
		} else {
			watchLogf("配置文件已更新：内容无变化")
		}
		return
	}

	// 不改正在被并发读的旧快照：克隆 → 搬热字段 → 原子发布。
	snapshot := Clone(oldCfg)
	applied := ApplyHotInto(snapshot, nextCfg)
	if len(applied) == 0 {
		return
	}
	PublishLive(snapshot)
	applyLogLevel(snapshot.Log.Level)

	if len(needRestart) > 0 {
		watchLogf("配置文件热重载成功：即时生效 %s；需重启后生效 %s",
			strings.Join(applied, ", "), strings.Join(needRestart, ", "))
	} else {
		watchLogf("配置文件热重载成功：即时生效 %s", strings.Join(applied, ", "))
	}
}

// sleepWithContext 等 d 这么久；ctx 取消时提前返回 false。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
