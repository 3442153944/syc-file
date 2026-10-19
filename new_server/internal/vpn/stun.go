package vpn

import (
	"fmt"
	"net"
	"strings"

	"go.uber.org/zap"

	"syc-file/pkg/logger"
)

// StartSTUN 启动组网的 STUN-lite：客户端向 UDP 端口发 "SYC?"，服务器把
// 「它看到的来源地址:端口」原样回 `SYC1 ip:port`。客户端拿到后随 heartbeat 上报，
// 对端就能拿到 NAT 后的真实端点去直连，而不是只能走中继。
//
// 协议刻意做成最小：不实现 RFC 5389，只解决「我自己的公网端点是多少」这一个问题。
func StartSTUN(port int) (net.PacketConn, error) {
	if port <= 0 {
		return nil, nil
	}
	conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("STUN-lite 监听 :%d 失败: %w", port, err)
	}
	go func() {
		buf := make([]byte, 256)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return // 连接关闭
			}
			if n < 4 || !strings.HasPrefix(string(buf[:n]), "SYC") {
				continue
			}
			_, _ = conn.WriteTo([]byte("SYC1 "+addr.String()), addr)
		}
	}()
	logger.Logger.Info("组网 STUN-lite 已启动", zap.Int("udp_port", port))
	return conn, nil
}
