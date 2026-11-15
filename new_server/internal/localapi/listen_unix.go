//go:build !windows

package localapi

import (
	"fmt"
	"net"
	"os"
	"time"
)

// listenLocal 在 Unix 上创建域套接字（HTTP 直接跑在这条字节流上）。
func listenLocal(endpoint string) (net.Listener, error) {
	// 上一次异常退出可能留下同名 socket；先清掉再监听。
	if err := os.Remove(endpoint); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("清理旧 Unix socket 失败: %w", err)
	}
	ln, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("监听 Unix socket %s 失败: %w", endpoint, err)
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(endpoint)
		return nil, fmt.Errorf("设置 Unix socket 权限失败: %w", err)
	}
	return ln, nil
}

// dialLocal 供测试/诊断使用：连接本机 Unix socket。
func dialLocal(endpoint string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, timeout)
}
