// Package desktoplaunch 负责「后端启动后自动拉起桌面控制台」。
//
// 约束（来自需求）：
//   - 后端启动后自动拉起 UI；UI 退出不影响后端；UI 单独启动不代表后端启动；
//   - Windows 服务（LocalSystem，session 0）启动时，控制台必须出现在当前登录用户的桌面上，
//     不能起在 session 0（用户看不见）；交互式启动则直接起子进程。
//
// 控制台自己用 QLocalServer 做单实例（同一份 config 只留一个窗口）：
// 这里不需要判断「是否已经在跑」，多起的那个实例会自己退出；后端只负责发起。
package desktoplaunch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Options 是启动控制台所需的最小信息（全部来自 config.gui 与后端启动路径）。
type Options struct {
	// Enabled 对应 gui.enabled；false 时直接跳过。
	Enabled bool
	// UIPath 对应 gui.ui_path；空 = 后端同目录的 syc-file-gui(.exe)。
	UIPath string
	// ConfigPath 当前生效的 config.yaml 绝对路径，传给控制台 --config。
	ConfigPath string
	// BackendExe 后端自己的可执行文件路径（用于定位同目录的控制台）。
	BackendExe string
}

// Result 描述本次自动拉起的结果（供后端日志区分「已拉起 / 主动跳过」）。
type Result struct {
	Launched   bool
	Executable string
	Reason     string
}

// Launch 按配置拉起桌面控制台。
//
// 返回 error 只表示「本该拉起但失败了」；没有图形会话、已关闭等属于正常跳过，返回 Result 说明原因。
func Launch(opts Options) (Result, error) {
	if !opts.Enabled {
		return Result{Reason: "gui.enabled=false"}, nil
	}
	exe, err := resolveUI(opts)
	if err != nil {
		return Result{}, err
	}
	if info, statErr := os.Stat(exe); statErr != nil || info.IsDir() {
		return Result{}, fmt.Errorf("控制台程序不存在: %s", exe)
	}
	args := []string{"--config", opts.ConfigPath, "--auto-start"}
	skipped, err := spawn(exe, args, filepath.Dir(exe))
	if err != nil {
		return Result{}, err
	}
	if skipped != "" {
		return Result{Reason: skipped}, nil
	}
	return Result{Launched: true, Executable: exe}, nil
}

// resolveUI 解析控制台可执行文件路径：绝对路径直接用；相对路径按后端 exe 目录解析；
// gui.ui_path 留空时用同目录的默认名字（平台相关）。
func resolveUI(opts Options) (string, error) {
	backendDir := filepath.Dir(opts.BackendExe)
	if backendDir == "" || backendDir == "." {
		if wd, err := os.Getwd(); err == nil {
			backendDir = wd
		}
	}
	path := strings.TrimSpace(opts.UIPath)
	if path == "" {
		name := "syc-file-gui"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path = filepath.Join(backendDir, name)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(backendDir, path)
	}
	return filepath.Clean(path), nil
}

// quoteArg 在 Windows 命令行里给参数加引号（路径可能含空格）。
func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t") && !strings.Contains(s, `"`) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
