//go:build !windows

package desktoplaunch

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawn 在 Linux/macOS 上拉起控制台：有图形会话才起，setsid 脱离后端进程组。
func spawn(exe string, args []string, workDir string) (string, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "当前环境没有图形会话（DISPLAY/WAYLAND_DISPLAY 为空）", nil
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动控制台失败: %w", err)
	}
	_ = cmd.Process.Release()
	return "", nil
}
