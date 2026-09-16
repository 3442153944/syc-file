// Package supervisor 托管后端依赖的外部进程（内网穿透 frpc、动态域名 ddns-go）。
//
// 为什么放进后端进程里：这几个东西以前靠一个 start.bat 甩出几个控制台窗口就不管了 ——
// 失败没人知道（脚本照样打印"启动完成"）、没有日志、进程死了不会重启。而它们一旦断掉，
// 外网就完全打不进来（两个域名都走 frp），表现却是"服务器好好的但访问不了"，极难排查。
// 后端本来就是这条链路上唯一长期驻留的进程，由它来看着这几个子进程最自然。
//
// 三条设计约束，都是踩过坑之后定的：
//
//  1. **已在运行就跳过**。frpc 用同样的 proxy 名重复连同一个 frps，服务端会拒绝
//     （`proxy [xxx] already exists`），结果是新旧两个客户端谁都用不上。所以启动前
//     先按可执行文件路径查一遍现有进程，用户手动起过就不抢。
//
//  2. **日志必须落盘**。子进程的 stdout/stderr 全部重定向到 log_dir 下，
//     否则出问题时什么线索都没有。
//
//  3. **主进程退出时一定要带走子进程**。否则后端重启一次就多一批孤儿 frpc，
//     再次触发第 1 条的 proxy 冲突。main 里必须调 Stop（见 graceful shutdown）。
package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/process"
	"go.uber.org/zap"
)

// ProcessConfig 一个被托管的外部进程。
type ProcessConfig struct {
	// Name 仅用于日志与日志文件名，需唯一
	Name string `mapstructure:"name"`
	// Enabled 为 false 时完全不启动，方便临时停掉某一个而不删配置
	Enabled bool `mapstructure:"enabled"`
	// Command 可执行文件路径。相对路径按后端工作目录解析。
	Command string `mapstructure:"command"`
	// Args 命令行参数。其中的相对路径同样按后端工作目录解析后再传给子进程 ——
	// 子进程的工作目录可能和后端不同（见 WorkDir），不转成绝对路径会找不到配置文件。
	Args []string `mapstructure:"args"`
	// WorkDir 子进程工作目录，留空则用可执行文件所在目录
	WorkDir string `mapstructure:"work_dir"`
	// SkipIfRunning 检测到同一个可执行文件已有进程在跑就跳过（frpc 必须开）
	SkipIfRunning bool `mapstructure:"skip_if_running"`
	// RestartDelaySeconds 异常退出后的重启间隔，<=0 表示不重启
	RestartDelaySeconds int `mapstructure:"restart_delay_seconds"`
}

// Config supervisor 总配置，对应 config.yaml 的 supervisor 段。
type Config struct {
	Enabled bool `mapstructure:"enabled"`
	// LogDir 子进程日志目录，相对后端工作目录
	LogDir    string          `mapstructure:"log_dir"`
	Processes []ProcessConfig `mapstructure:"processes"`
}

// Supervisor 持有所有被托管进程的生命周期。
type Supervisor struct {
	cfg Config
	// log 由调用方注入：supervisor 不能 import pkg/logger，
	// 那会造成 config → supervisor → logger → config 的循环导入。
	log    *zap.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running map[string]*exec.Cmd // name -> 当前进程，供 Stop 时逐个终止
}

// New 只做构造，不启动任何东西。log 传 nil 时静默运行。
func New(cfg Config, log *zap.Logger) *Supervisor {
	if log == nil {
		log = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		cfg:     cfg,
		log:     log,
		ctx:     ctx,
		cancel:  cancel,
		running: make(map[string]*exec.Cmd),
	}
}

// Start 拉起所有启用的进程，每个进程一个守护 goroutine。非阻塞。
func (s *Supervisor) Start() {
	if !s.cfg.Enabled {
		s.log.Info("外部进程托管未启用，跳过")
		return
	}
	if s.cfg.LogDir != "" {
		if err := os.MkdirAll(s.cfg.LogDir, 0o755); err != nil {
			s.log.Warn("创建子进程日志目录失败，日志将被丢弃",
				zap.String("dir", s.cfg.LogDir), zap.Error(err))
		}
	}
	for _, pc := range s.cfg.Processes {
		if !pc.Enabled {
			s.log.Info("外部进程已禁用，跳过", zap.String("name", pc.Name))
			continue
		}
		if pc.Name == "" || pc.Command == "" {
			s.log.Warn("外部进程配置不完整（缺 name 或 command），跳过",
				zap.String("name", pc.Name))
			continue
		}
		s.wg.Add(1)
		go s.supervise(pc)
	}
}

// supervise 单个进程的守护循环：启动 → 等待退出 → 按需重启，直到 ctx 被取消。
func (s *Supervisor) supervise(pc ProcessConfig) {
	defer s.wg.Done()

	// 用户已经手动起过就不抢。这里只在首次启动时判断：后续由我们拉起的进程
	// 死掉时理应重启，不该因为"检测到自己刚死剩下的同名残留"而放弃。
	if pc.SkipIfRunning {
		if pid, ok := findRunning(pc.Command, pc.Args); ok {
			s.log.Info("外部进程已在运行，交由现有实例接管",
				zap.String("name", pc.Name), zap.Int32("pid", pid))
			return
		}
	}

	for {
		if s.ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := s.runOnce(pc)
		if s.ctx.Err() != nil {
			return // 是我们主动停的，不算异常
		}
		if err != nil {
			s.log.Warn("外部进程退出",
				zap.String("name", pc.Name),
				zap.Duration("uptime", time.Since(start)),
				zap.Error(err))
		} else {
			s.log.Warn("外部进程自行退出",
				zap.String("name", pc.Name),
				zap.Duration("uptime", time.Since(start)))
		}

		if pc.RestartDelaySeconds <= 0 {
			s.log.Info("该进程未配置重启，不再拉起", zap.String("name", pc.Name))
			return
		}
		delay := time.Duration(pc.RestartDelaySeconds) * time.Second
		s.log.Info("准备重启外部进程",
			zap.String("name", pc.Name), zap.Duration("after", delay))
		select {
		case <-time.After(delay):
		case <-s.ctx.Done():
			return
		}
	}
}

// runOnce 启动一次并阻塞到进程退出。
func (s *Supervisor) runOnce(pc ProcessConfig) error {
	cmdPath, err := filepath.Abs(pc.Command)
	if err != nil {
		return fmt.Errorf("解析可执行文件路径失败: %w", err)
	}
	if _, err := os.Stat(cmdPath); err != nil {
		return fmt.Errorf("可执行文件不存在: %s", cmdPath)
	}

	// 参数里的相对路径要转绝对：子进程的工作目录通常是它自己的安装目录，
	// 而配置文件放在本项目的 deploy/ 下，不转换会找不到。
	args := make([]string, len(pc.Args))
	for i, a := range pc.Args {
		args[i] = resolveArg(a)
	}

	workDir := pc.WorkDir
	if workDir == "" {
		workDir = filepath.Dir(cmdPath)
	}

	cmd := exec.Command(cmdPath, args...)
	cmd.Dir = workDir

	if logFile, err := s.openLog(pc.Name); err != nil {
		s.log.Warn("打开子进程日志失败，输出将被丢弃",
			zap.String("name", pc.Name), zap.Error(err))
	} else {
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	s.log.Info("外部进程已启动",
		zap.String("name", pc.Name),
		zap.Int("pid", cmd.Process.Pid),
		zap.String("cmd", cmdPath),
		zap.Strings("args", args))

	s.mu.Lock()
	s.running[pc.Name] = cmd
	s.mu.Unlock()

	err = cmd.Wait()

	s.mu.Lock()
	delete(s.running, pc.Name)
	s.mu.Unlock()
	return err
}

// openLog 以追加方式打开子进程日志文件。
func (s *Supervisor) openLog(name string) (*os.File, error) {
	if s.cfg.LogDir == "" {
		return nil, fmt.Errorf("未配置 log_dir")
	}
	p := filepath.Join(s.cfg.LogDir, sanitize(name)+".log")
	return os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// Stop 终止所有被托管的进程并等待守护 goroutine 结束。
//
// 必须在主进程退出前调用：留下孤儿 frpc 会让下次启动撞上 frps 的 proxy 冲突。
// 这里用 Kill 而不是优雅信号 —— Windows 上给子进程发 CTRL_BREAK 要单独建进程组，
// 而 frpc/ddns-go 被 TerminateProcess 干掉时，OS 会关掉它们的 TCP 连接，
// frps 那边能立刻感知到并清掉 proxy 注册，效果够用。
func (s *Supervisor) Stop(timeout time.Duration) {
	if !s.cfg.Enabled {
		return
	}
	s.cancel()

	s.mu.Lock()
	for name, cmd := range s.running {
		if cmd.Process == nil {
			continue
		}
		s.log.Info("正在终止外部进程",
			zap.String("name", name), zap.Int("pid", cmd.Process.Pid))
		if err := cmd.Process.Kill(); err != nil {
			s.log.Warn("终止外部进程失败",
				zap.String("name", name), zap.Error(err))
		}
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.log.Info("所有外部进程已停止")
	case <-time.After(timeout):
		s.log.Warn("等待外部进程退出超时，放弃等待",
			zap.Duration("timeout", timeout))
	}
}

// findRunning 查找已在运行的同一个进程实例，返回首个匹配的 pid。
//
// 光比可执行文件路径不够：两个 frpc 实例用的是同一个 frpc.exe，只是 -c 指向不同的
// toml，只比 exe 的话，起了 ddns 那条就会把东京那条也误判成"已在运行"而跳过。
// 所以再用参数里的文件名（frpc.toml / frpc_akile.toml）作为区分标识去比命令行。
//
// 路径比较统一成小写并归一分隔符：Windows 上同一个文件可能以不同大小写/斜杠出现。
func findRunning(command string, args []string) (int32, bool) {
	target, err := filepath.Abs(command)
	if err != nil {
		return 0, false
	}
	target = normalizePath(target)

	// 从参数里挑出像文件名的部分当标识，只取 basename ——
	// 现有进程多半是用相对路径起的（frpc.exe -c frpc.toml），比全路径匹配不上。
	var markers []string
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		if base := filepath.Base(a); strings.Contains(base, ".") {
			markers = append(markers, strings.ToLower(base))
		}
	}

	procs, err := process.Processes()
	if err != nil {
		return 0, false
	}
	self := int32(os.Getpid())
	for _, p := range procs {
		if p.Pid == self {
			continue
		}
		exe, err := p.Exe()
		if err != nil || exe == "" {
			continue // 多数是权限不足读不到别的用户的进程，跳过即可
		}
		if normalizePath(exe) != target {
			continue
		}
		if len(markers) == 0 {
			return p.Pid, true // 没有可区分的参数，认 exe 即可
		}
		cmdline, err := p.Cmdline()
		if err != nil {
			continue // 读不到命令行就没法判断是不是同一个实例，宁可当成不是
		}
		cl := strings.ToLower(cmdline)
		matched := true
		for _, m := range markers {
			if !strings.Contains(cl, m) {
				matched = false
				break
			}
		}
		if matched {
			return p.Pid, true
		}
	}
	return 0, false
}

func normalizePath(p string) string {
	return strings.ToLower(filepath.Clean(strings.ReplaceAll(p, "/", string(filepath.Separator))))
}

// resolveArg 把看起来像「已存在的相对路径」的参数转成绝对路径，其余原样返回。
//
// 只转换实际存在的路径，避免把 `-c`、`start` 这类普通参数误当成路径。
func resolveArg(a string) string {
	if a == "" || strings.HasPrefix(a, "-") || filepath.IsAbs(a) {
		return a
	}
	if _, err := os.Stat(a); err != nil {
		return a
	}
	abs, err := filepath.Abs(a)
	if err != nil {
		return a
	}
	return abs
}

// sanitize 去掉文件名里的非法字符，供日志文件名使用。
func sanitize(name string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	return r.Replace(name)
}
