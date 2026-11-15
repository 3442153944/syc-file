//go:build windows

package localapi

import (
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// pipeSecurityDescriptor 允许：
//   - SYSTEM / Administrators 完全控制（后端服务本身以 LocalSystem 运行）；
//   - Authenticated Users 读写（当前登录用户的桌面控制台要能连进来）。
//
// 不能依赖 go-winio 的默认 DACL：默认 ACL 只给创建者（服务进程 = SYSTEM），
// 普通用户启动的 GUI 会得到 Access Denied。
const pipeSecurityDescriptor = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)"

// listenLocal 在 Windows 上创建命名管道监听器（HTTP 直接跑在这条字节流上）。
func listenLocal(endpoint string) (net.Listener, error) {
	ln, err := winio.ListenPipe(pipePath(endpoint), &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor,
	})
	if err != nil {
		return nil, fmt.Errorf("监听命名管道 %s 失败: %w", pipePath(endpoint), err)
	}
	return ln, nil
}

// dialLocal 供测试/诊断使用：连接本机命名管道。
func dialLocal(endpoint string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(pipePath(endpoint), &timeout)
}
