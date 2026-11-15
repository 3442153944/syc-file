// Package localapi 实现「桌面控制台 ↔ 后端进程」的本地管理接口。
//
// 定位：GUI 与后端是两个独立进程（见 internal/desktopapi 的包注释），配置和日志都走文件，
// 只有「实时状态 + 重启/停止」需要跨进程通道 —— 就是这个包：后端在**本机 IPC**
// （Windows 命名管道 / Linux Unix 域套接字）上跑一个很小的 HTTP 服务，
// 并把 {pid, ipc, token} 写到 runtime.json（权限 0600），GUI 读该文件后带 token 调用。
//
// 为什么不用 TCP loopback：代理软件的 TUN/透明接管可能把发往 127.0.0.1 的流量劫走，
// 命名管道/Unix socket 不经过 IP 栈，代理无从接管；同时也不占用端口、不暴露给外部网络。
//
// 安全边界：
//   - 命名管道只授予 SYSTEM/Administrators/已认证用户（见 listen_windows.go 的 DACL），
//     Unix socket 权限 0600，外部网络打不到；
//   - 每次启动随机 32 hex token，写在 0600 的 runtime.json 里，进程退出即删除；
//   - 只暴露管理接口（状态/重启/停止/配置校验/配置表单），不承载任何业务能力。
package localapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"syc-file/config"
	"syc-file/internal/desktopapi"
)

// TokenHeader 是调用本地管理接口时必须携带的请求头名字。
const TokenHeader = "X-Syc-Local-Token"

// RuntimeFileName 是运行时信息文件名，固定放在 config.yaml 所在目录。
const RuntimeFileName = "runtime.json"

// Handlers 是本地管理接口需要后端提供的操作。
//
// Status 会被 GUI 的高频轮询调用，实现方要保证并发安全且足够快（内部做短超时 Ping）。
type Handlers interface {
	// Status 返回当前运行状态快照。
	Status() desktopapi.Status
	// Restart 请求重启：实现方只负责「安排退出」，由主循环决定随后如何重启。
	Restart()
	// Shutdown 请求优雅退出。
	Shutdown()
	// ValidateConfig 校验一份待保存的 YAML 文本：GUI 保存前先问后端能不能用。
	// 只解析+校验，不落盘、不应用。
	ValidateConfig(text string) error
	// ConfigFields 返回配置表单的字段目录（分组/标签/类型/当前值/热字段标记）。
	ConfigFields() (desktopapi.ConfigView, error)
	// SaveConfigFields 保存一组字段修改（点分路径 → 文本值），保留注释并备份。
	// 返回备份路径与「立即生效 / 需重启」分类。
	SaveConfigFields(values map[string]string) (desktopapi.SaveFieldsResult, error)
}

// RuntimeInfo 是 backend 启动时写到 runtime.json 的内容，GUI 据此连接。
//
// IPC 的取值随平台不同：
//   - Windows：命名管道名（如 syc-file-localapi-1a2b3c4d5e6f7a8b），GUI 用 QLocalSocket 同名连接；
//   - Linux/macOS：Unix domain socket 的绝对路径（权限 0600），GUI 用 QLocalSocket 连接同一路径。
type RuntimeInfo struct {
	PID        int       `json:"pid"`
	IPC        string    `json:"ipc"`
	Token      string    `json:"token"`
	StartedAt  time.Time `json:"started_at"`
	Version    string    `json:"version"`
	Mode       string    `json:"mode"`
	ConfigPath string    `json:"config_path"`
	WorkingDir string    `json:"working_dir"`
}

// Server 是一个已启动的本地管理接口。
type Server struct {
	info        RuntimeInfo
	ln          net.Listener
	srv         *http.Server
	runtimePath string
}

// Start 在本机 IPC 端点（命名管道 / Unix socket）上启动管理接口，并把运行时信息写入 dir/runtime.json。
// dir 通常是 config.yaml 所在目录（GUI 也知道这个位置，不需要额外发现机制）。
func Start(dir string, h Handlers) (*Server, error) {
	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("生成本地管理 token 失败: %w", err)
	}
	endpoint, err := newEndpoint()
	if err != nil {
		return nil, err
	}
	ln, err := listenLocal(endpoint)
	if err != nil {
		return nil, err
	}
	mode := config.Conf.Server.Mode
	if mode == "" {
		// 单元测试/未跑 config.Init 的进程里 Conf 是零值，给一个可展示的默认模式。
		mode = config.ModeDev
	}
	info := RuntimeInfo{
		PID:        os.Getpid(),
		IPC:        endpoint,
		Token:      token,
		StartedAt:  time.Now(),
		Version:    config.Version,
		Mode:       mode,
		ConfigPath: filepath.Join(dir, "config.yaml"),
		WorkingDir: mustGetwd(),
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	info.ConfigPath = filepath.Join(absDir, "config.yaml")
	runtimePath := filepath.Join(absDir, RuntimeFileName)

	s := &Server{info: info, ln: ln, runtimePath: runtimePath}
	s.srv = &http.Server{
		Handler:           s.routes(h),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := s.writeRuntime(); err != nil {
		_ = ln.Close()
		return nil, err
	}
	go func() {
		// 关闭时 ListenAndServe 返回 ErrServerClosed，属正常路径，无需记录。
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// Endpoint 返回本机 IPC 端点（Windows 命名管道名 / Unix socket 路径）。
func (s *Server) Endpoint() string { return s.info.IPC }

// RuntimePath 返回 runtime.json 的绝对路径。
func (s *Server) RuntimePath() string { return s.runtimePath }

// Close 停止监听并删除 runtime.json（尽力而为：文件删不掉不影响进程退出）。
func (s *Server) Close() error {
	err := s.srv.Close()
	if rmErr := os.Remove(s.runtimePath); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = rmErr
	}
	return err
}

// routes 注册管理接口，全部要求 token 头。
func (s *Server) routes(h Handlers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, h.Status())
	}))
	mux.HandleFunc("/restart", s.auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"result": "restarting"})
		// 先让响应回到调用方，再安排退出，避免 GUI 拿到连接重置而不是 202。
		go h.Restart()
	}))
	mux.HandleFunc("/shutdown", s.auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"result": "shutting down"})
		go h.Shutdown()
	}))
	// 配置预校验：桌面控制台保存前先问后端「这份 YAML 能不能用」，避免写坏配置。
	mux.HandleFunc("/config/validate", s.auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取请求体失败: " + err.Error()})
			return
		}
		if err := h.ValidateConfig(string(body)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
	}))
	// 配置表单：GET 取字段目录（含当前值），POST {"values":{path:value}} 保存修改。
	mux.HandleFunc("/config/fields", s.auth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			view, err := h.ConfigFields()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, view)
		case http.MethodPost:
			var req struct {
				Values map[string]string `json:"values"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体解析失败: " + err.Error()})
				return
			}
			if len(req.Values) == 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有要保存的字段"})
				return
			}
			result, err := h.SaveConfigFields(req.Values)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, result)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	return mux
}

// auth 校验 token 头；比较用常量时间，避免按字节泄露。
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(TokenHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.info.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		next(w, r)
	}
}

// writeRuntime 原子写入 runtime.json（先写临时文件再 rename），权限 0600。
func (s *Server) writeRuntime() error {
	if err := os.MkdirAll(filepath.Dir(s.runtimePath), 0o755); err != nil {
		return fmt.Errorf("创建运行时信息目录失败: %w", err)
	}
	data, err := json.MarshalIndent(s.info, "", "  ")
	if err != nil {
		return fmt.Errorf("编码运行时信息失败: %w", err)
	}
	tmp := s.runtimePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入运行时信息失败: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		// Windows 上 Chmod 只影响只读位，失败也不影响功能。
		_ = err
	}
	if err := os.Rename(tmp, s.runtimePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换运行时信息失败: %w", err)
	}
	return nil
}

// ReadRuntime 读取并解析 runtime.json；文件不存在或内容不完整都返回错误。
func newToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(wd)
	if err != nil {
		return wd
	}
	return abs
}
