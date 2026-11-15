package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"syc-file/internal/desktopapi"
)

// fakeHandlers 记录管理接口收到的动作，返回固定状态。
type fakeHandlers struct {
	mu        sync.Mutex
	restarts  int
	shutdowns int
}

func (f *fakeHandlers) Status() desktopapi.Status {
	return desktopapi.Status{Version: "test-version", Mode: "dev", PID: 1234, OnlineConns: 2, Running: true}
}

func (f *fakeHandlers) Restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
}

func (f *fakeHandlers) Shutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
}

// ValidateConfig 里含 "bad" 就当成非法 YAML，供接口测试用。
func (f *fakeHandlers) ValidateConfig(text string) error {
	if strings.Contains(text, "bad") {
		return errors.New("配置校验失败: 测试用非法内容")
	}
	return nil
}

// ConfigFields 返回一份最小字段目录，覆盖 GET /config/fields 的序列化路径。
func (f *fakeHandlers) ConfigFields() (desktopapi.ConfigView, error) {
	return desktopapi.ConfigView{
		Path: "/tmp/config.yaml",
		Fields: []desktopapi.ConfigField{{
			Path:      "server.port",
			Label:     "HTTP 端口",
			Group:     "服务",
			Kind:      desktopapi.KindInt,
			Value:     "8991",
			HotReload: false,
		}},
	}, nil
}

// SaveConfigFields 用 "bad.path" 模拟未知字段被拒绝，其余字段返回热/冷分类。
func (f *fakeHandlers) SaveConfigFields(values map[string]string) (desktopapi.SaveFieldsResult, error) {
	if _, ok := values["bad.path"]; ok {
		return desktopapi.SaveFieldsResult{}, errors.New("未知或只读的配置项: bad.path")
	}
	return desktopapi.SaveFieldsResult{
		BackupPath:  "/tmp/config.yaml.bak-20261115-175500",
		HotApplied:  []string{"log.level"},
		NeedRestart: []string{"server.port"},
	}, nil
}

// readRuntimeInfo 直接从磁盘解析 runtime.json（生产环境的 GUI 也这么读）。
func readRuntimeInfo(t *testing.T, path string) RuntimeInfo {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 runtime.json: %v", err)
	}
	var info RuntimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("解析 runtime.json: %v", err)
	}
	return info
}

// TestStartServesStatusAndActions 覆盖启动 → 写 runtime.json → 带 token 调接口 → 关闭清理。
func TestStartServesStatusAndActions(t *testing.T) {
	dir := t.TempDir()
	h := &fakeHandlers{}

	s, err := Start(dir, h)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	runtimePath := filepath.Join(dir, RuntimeFileName)
	info := readRuntimeInfo(t, runtimePath)
	if info.IPC == "" || len(info.Token) != 32 {
		t.Fatalf("runtime.json 内容不对: %+v", info)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if runtime.GOOS != "windows" {
		st, statErr := os.Stat(runtimePath)
		if statErr != nil {
			t.Fatalf("stat runtime.json: %v", statErr)
		}
		if mode := st.Mode().Perm(); mode != 0o600 {
			t.Fatalf("runtime.json 权限 = %o, want 600", mode)
		}
	}

	// IPC 端点承载 HTTP：Host 是占位，实际连接走命名管道 / Unix socket。
	baseURL := "http://syc-file-local"
	client := testHTTPClient(t, s)
	statusURL := baseURL + "/status"

	// 无 token / 错 token 都要 401。
	for _, token := range []string{"", "wrong-token"} {
		req, _ := http.NewRequest(http.MethodGet, statusURL, nil)
		if token != "" {
			req.Header.Set(TokenHeader, token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("请求 status: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token=%q 状态码 = %d, want 401", token, resp.StatusCode)
		}
	}

	// 正确 token：/status 返回 JSON。
	req, _ := http.NewRequest(http.MethodGet, statusURL, nil)
	req.Header.Set(TokenHeader, info.Token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 status: %v", err)
	}
	var status desktopapi.Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("解码 status: %v", err)
	}
	_ = resp.Body.Close()
	if status.Version != "test-version" || status.PID != 1234 || status.OnlineConns != 2 {
		t.Fatalf("status 内容不对: %+v", status)
	}

	// restart / shutdown 各调一次。
	for _, path := range []string{"/restart", "/shutdown"} {
		req, _ := http.NewRequest(http.MethodPost, baseURL+path, nil)
		req.Header.Set(TokenHeader, info.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %s 状态码 = %d, want 202", path, resp.StatusCode)
		}
	}
	waitFor(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.restarts == 1 && h.shutdowns == 1
	})

	// 配置预校验：合法内容 200，非法内容 400。
	for _, tc := range []struct {
		body string
		code int
	}{
		{"server:\n  port: 8991\n", http.StatusOK},
		{"bad: [", http.StatusBadRequest},
	} {
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/config/validate", strings.NewReader(tc.body))
		req.Header.Set(TokenHeader, info.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /config/validate: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.code {
			t.Fatalf("validate(%q) 状态码 = %d, want %d", tc.body, resp.StatusCode, tc.code)
		}
	}

	// 配置表单：GET 字段目录、POST 保存字段（未知字段 400）。
	req, _ = http.NewRequest(http.MethodGet, baseURL+"/config/fields", nil)
	req.Header.Set(TokenHeader, info.Token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /config/fields: %v", err)
	}
	var view desktopapi.ConfigView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("解码 /config/fields: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(view.Fields) != 1 || view.Fields[0].Path != "server.port" {
		t.Fatalf("字段目录不对: code=%d view=%+v", resp.StatusCode, view)
	}

	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"values":{"server.port":"9000"}}`, http.StatusOK},
		{`{"values":{"bad.path":"x"}}`, http.StatusBadRequest},
	} {
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/config/fields", strings.NewReader(tc.body))
		req.Header.Set(TokenHeader, info.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /config/fields: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.code {
			t.Fatalf("POST /config/fields %s 状态码 = %d, want %d: %s", tc.body, resp.StatusCode, tc.code, body)
		}
		if tc.code == http.StatusOK {
			var result desktopapi.SaveFieldsResult
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatalf("解码保存结果: %v", err)
			}
			if len(result.HotApplied) != 1 || len(result.NeedRestart) != 1 {
				t.Fatalf("保存结果热/冷分类不对: %+v", result)
			}
		}
	}
}

// TestCloseRemovesRuntimeFile 验证退出后 runtime.json 被删除（GUI 不会连到死进程）。
func TestCloseRemovesRuntimeFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(dir, &fakeHandlers{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	runtimePath := filepath.Join(dir, RuntimeFileName)
	if _, err := os.Stat(runtimePath); err != nil {
		t.Fatalf("启动后 runtime.json 应存在: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(runtimePath); !os.IsNotExist(err) {
		t.Fatalf("关闭后 runtime.json 应被删除，err=%v", err)
	}
}

// TestRuntimeFileHasRequiredFields 验证 runtime.json 里 GUI 依赖的字段齐全。
func TestRuntimeFileHasRequiredFields(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(dir, &fakeHandlers{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	info := readRuntimeInfo(t, filepath.Join(dir, RuntimeFileName))
	if info.IPC == "" || info.IPC != s.Endpoint() {
		t.Fatalf("ipc = %q, Server.Endpoint() = %q", info.IPC, s.Endpoint())
	}
	if info.Version == "" || info.Mode == "" || info.ConfigPath == "" || info.WorkingDir == "" {
		t.Fatalf("runtime.json 字段不全: %+v", info)
	}
}

// waitFor 轮询等待断言成立（最多 2s）。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件在 2s 内未成立")
}

// testHTTPClient 用 IPC 端点承载 HTTP：测试里照常发 http:// 请求，只是 DialContext 换成管道/socket。
func testHTTPClient(t *testing.T, s *Server) *http.Client {
	t.Helper()
	endpoint := s.Endpoint()
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return dialLocal(endpoint, 2*time.Second)
			},
		},
	}
}
