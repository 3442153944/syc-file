//go:build windows

package desktoplaunch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// spawn 按当前进程所在会话决定启动方式：
//   - session 0（Windows 服务）：用 WTSQueryUserToken + CreateProcessAsUser 落到登录用户的桌面；
//   - 交互式会话（用户自己启动后端）：直接拉起子进程。
//
// 返回的跳过原因非空表示「正常跳过」（例如服务启动时还没有用户登录）。
func spawn(exe string, args []string, workDir string) (string, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		session = 0
	}
	if session == 0 {
		return spawnInActiveSession(exe, args, workDir)
	}
	return "", spawnInteractive(exe, args, workDir)
}

// spawnInteractive 在同一个交互式会话里起控制台（后端由用户手动启动的常见路径）。
func spawnInteractive(exe string, args []string, workDir string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = workDir
	// DETACHED_PROCESS：不继承后端控制台；GUI 子系统本身也不会弹黑框。
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动控制台失败: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

// spawnInActiveSession 在 Windows 服务的 session 0 里，把控制台放进当前登录用户的桌面。
//
// 依赖 LocalSystem 自带的 SeTcbPrivilege（WTSQueryUserToken 需要）；普通管理员权限不足时
// 该调用会失败，此时返回错误由调用方记日志（登录自启动兜底）。
func spawnInActiveSession(exe string, args []string, workDir string) (string, error) {
	sessionID := windows.WTSGetActiveConsoleSessionId()
	if sessionID == 0xFFFFFFFF {
		return "当前没有已登录的交互式用户（服务启动早于用户登录）", nil
	}

	var userToken windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &userToken); err != nil {
		return "", fmt.Errorf("获取登录用户令牌失败(session=%d): %w", sessionID, err)
	}
	defer userToken.Close()

	var primary windows.Token
	if err := windows.DuplicateTokenEx(userToken, windows.TOKEN_ALL_ACCESS, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return "", fmt.Errorf("复制登录用户令牌失败: %w", err)
	}
	defer primary.Close()

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, primary, false); err == nil && env != nil {
		defer windows.DestroyEnvironmentBlock(env)
	} else {
		env = nil
	}

	commandLine, err := windows.UTF16PtrFromString(buildCommandLine(exe, args))
	if err != nil {
		return "", fmt.Errorf("构造控制台命令行失败: %w", err)
	}
	desktop, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return "", fmt.Errorf("构造桌面名失败: %w", err)
	}
	var dirPtr *uint16
	if workDir != "" {
		if dirPtr, err = windows.UTF16PtrFromString(workDir); err != nil {
			return "", fmt.Errorf("构造工作目录失败: %w", err)
		}
	}

	startupInfo := &windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	startupInfo.Desktop = desktop

	var processInfo windows.ProcessInformation
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	if err := windows.CreateProcessAsUser(primary, nil, commandLine, nil, nil, false, flags,
		env, dirPtr, startupInfo, &processInfo); err != nil {
		return "", fmt.Errorf("在登录用户桌面启动控制台失败: %w", err)
	}
	_ = windows.CloseHandle(processInfo.Thread)
	_ = windows.CloseHandle(processInfo.Process)
	return "", nil
}

func buildCommandLine(exe string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArg(filepath.Clean(exe)))
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}
