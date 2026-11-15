package vpn

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// maxLANEndpoints 一次心跳最多接受上报的 LAN 候选数；客户端（Android）自身也只保留 8 个。
const maxLANEndpoints = 8

// validateLANEndpoints 校验客户端上报的 LAN 候选端点：只接受私网 IP:port。
// 客户端必须用登录后的真实身份上报，不能拿 HTTP 代理/入口地址冒充 LAN 候选，
// 否则其它 peer 会朝回环、公网或代理地址打洞。读取时再校验一次是为了不信任库里
// 可能由旧版本写入的脏数据。
func validateLANEndpoints(values []string) ([]string, error) {
	if len(values) > maxLANEndpoints {
		return nil, fmt.Errorf("最多上报 %d 个 LAN 端点", maxLANEndpoints)
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		ap, err := netip.ParseAddrPort(strings.TrimSpace(value))
		if err != nil || ap.Port() == 0 || !ap.Addr().IsPrivate() || ap.Addr().IsLoopback() || ap.Addr().Zone() != "" {
			return nil, fmt.Errorf("LAN 端点必须是有效私网 IP:port")
		}
		endpoint := ap.String()
		if !seen[endpoint] {
			out = append(out, endpoint)
			seen[endpoint] = true
		}
	}
	return out, nil
}

// endpointCandidates 按「客户端上报的 LAN 候选 → 服务器实际观察到的端点 → 客户端上报的端点」
// 顺序合并去重。LAN 排在最前，让同 LAN 的 peer 先试直连；回环（经 frp 中继时服务器只能看到
// 127.0.0.1）和未指定地址一律丢弃，避免客户端朝错误地址打洞。
func endpointCandidates(lanJSON, observed, reported string) []string {
	var lan []string
	// 库里存的 JSON 由本服务写入并已校验；即使损坏也只按「没有 LAN 候选」处理，
	// 不能让 /peers 整体失败。
	_ = json.Unmarshal([]byte(lanJSON), &lan)
	lan, _ = validateLANEndpoints(lan)
	out := make([]string, 0, len(lan)+2)
	for _, value := range lan {
		out = appendEndpoint(out, value)
	}
	for _, value := range []string{observed, reported} {
		out = appendEndpoint(out, value)
	}
	return out
}

// serverCandidates 生成服务器节点的候选端点：配置的 LAN 端点在前，再补上按本次请求
// 入口推导出的端点（X-Forwarded-Host/Host + ServerWGPort）。空值、回环、未指定地址
// 与重复项会被过滤，避免下发无意义的候选。
func serverCandidates(lanEndpoints []string, endpoint string) []string {
	out := make([]string, 0, len(lanEndpoints)+1)
	for _, value := range lanEndpoints {
		out = appendEndpoint(out, value)
	}
	return appendEndpoint(out, endpoint)
}

// appendEndpoint 把合法的 IP:port 规范化后追加到 out（去重），非法值直接忽略。
func appendEndpoint(out []string, value string) []string {
	ap, err := netip.ParseAddrPort(strings.TrimSpace(value))
	if err != nil || ap.Port() == 0 || ap.Addr().IsLoopback() || ap.Addr().IsUnspecified() {
		return out
	}
	endpoint := ap.String()
	for _, entry := range out {
		if entry == endpoint {
			return out
		}
	}
	return append(out, endpoint)
}
