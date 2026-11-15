package localapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// endpointPrefix 是 Windows 命名管道名（不含 `\\.\pipe\` 前缀）。
const endpointPrefix = "syc-file-localapi-"

// newEndpoint 生成本次进程专用的本地端点：
//   - Windows：命名管道名 `syc-file-localapi-<16hex>`（全局命名空间）；
//   - 其他平台：/tmp/syc-file-<uid>-<16hex>.sock（权限 0600）。
//
// 每次启动随机生成，既避免与旧进程/别的实例撞名，也让 runtime.json 成为唯一发现入口。
func newEndpoint() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 IPC 端点随机名失败: %w", err)
	}
	suffix := hex.EncodeToString(buf)
	if runtime.GOOS == "windows" {
		return endpointPrefix + suffix, nil
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("syc-file-%d-%s.sock", os.Getuid(), suffix)), nil
}

// pipePath 把端点补成 Windows 完整管道路径；其他平台原样返回。
func pipePath(endpoint string) string {
	if runtime.GOOS == "windows" && !strings.HasPrefix(endpoint, `\\.\pipe\`) {
		return `\\.\pipe\` + endpoint
	}
	return endpoint
}
