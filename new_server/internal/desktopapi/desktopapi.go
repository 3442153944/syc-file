// Package desktopapi 是「桌面控制台」与后端进程之间的线上契约类型。
//
// 架构（2026-11-15 第二版：GUI 独立为 C++/Qt6 项目 server_ui/）：
//   - syc-file      后端服务本体（headless，常驻），完全不依赖 GUI；
//   - server_ui     独立桌面控制台（Qt6），只通过 config.yaml / 日志文件 / 本地管理接口通信，
//     不 import 后端 Go 代码。
//
// 本包只保留本地管理接口（internal/localapi）在 HTTP 上回给 GUI 的数据类型。
// 约束：只能依赖标准库。
package desktopapi

import "time"

// ExitReason 是后端主循环退出的原因：决定退出后是否需要由外部重新拉起。
type ExitReason string

const (
	// ExitQuit 正常退出（系统信号 / 启动器「停止后端」）。
	ExitQuit ExitReason = "quit"
	// ExitRestart 请求重启：本进程优雅退出后，由桌面控制台或部署层（systemd/docker/nssm）重新拉起。
	ExitRestart ExitReason = "restart"
)

// Status 是概览页要展示的一次性运行状态快照（GET /status 的 JSON 体）。
//
// 刻意保持扁平、可直接展示：GUI 不做二次推导，只把字段摆出来。
// 拿不到的字段留零值，由 GUI 显示为「—」。
type Status struct {
	// Version 后端版本号（config.Version）。
	Version string `json:"version"`
	// Mode 运行模式：dev / prod。
	Mode string `json:"mode"`
	// NodeName 节点名（server.name；留空时后端用主机名兜底，这里给实际生效值）。
	NodeName string `json:"node_name"`
	// Platform / Arch / GoVersion 运行时信息，便于确认「这份 GUI 管的是哪个进程」。
	Platform  string `json:"platform"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`

	// PID 后端进程号。
	PID int `json:"pid"`
	// StartedAt 进程启动时间；Uptime 为已运行时长（纳秒，JSON number）。
	StartedAt time.Time     `json:"started_at"`
	Uptime    time.Duration `json:"uptime"`
	// Running 后端进程当前是否在运行（本地接口回包恒为 true；离线快照由 GUI 自己造）。
	Running bool `json:"running"`

	// Port HTTP 监听端口。
	Port int `json:"port"`

	// ConfigPath 当前生效的配置文件绝对路径。
	ConfigPath string `json:"config_path"`
	// LogDir 后端日志目录绝对路径。
	LogDir string `json:"log_dir"`
	// WorkingDir 进程工作目录：配置里 ./log、./config 这类相对路径都相对它解析。
	WorkingDir string `json:"working_dir"`

	// MemoryBytes 本进程常驻内存（取不到为 0）。
	MemoryBytes uint64 `json:"memory_bytes"`
	// Goroutines 本进程 goroutine 数（取不到为 0）。
	Goroutines int `json:"goroutines"`

	// OnlineConns 当前 WebSocket 在线连接数（取不到为 0）。
	OnlineConns int `json:"online_conns"`

	// DBOK / RedisOK 依赖可用性：概览页一眼看出「服务起了但数据库没连上」。
	DBOK    bool `json:"db_ok"`
	RedisOK bool `json:"redis_ok"`

	// Supervisor 被托管的外部进程（frpc / ddns-go / cloudflared）状态。
	// 未启用托管时为空切片。
	Supervisor []ProcessStatus `json:"supervisor,omitempty"`

	// Extra 预留的补充信息（键值对），GUI 按原样列出。
	Extra []KV `json:"extra,omitempty"`
}

// KV 是概览页上的一行补充信息。
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ProcessStatus 是托管子进程的当前状态。
type ProcessStatus struct {
	Name string `json:"name"`
	// Running 是否在跑；PID 在 Running 为 false 时为 0。
	Running bool `json:"running"`
	PID     int  `json:"pid"`
	// Restarts 本进程生命周期内被拉起的总次数，反复重启说明配置或网络有问题。
	Restarts int `json:"restarts"`
	// LastError 最近一次启动失败的原因（成功启动过则为空）。
	LastError string `json:"last_error"`
}

// ---- 配置表单（GUI 读取字段目录 / 保存修改）--------------------------------

// FieldKind 是配置字段在 GUI 上应该用哪种控件渲染。
type FieldKind string

const (
	KindString FieldKind = "string" // 单行文本
	KindInt    FieldKind = "int"    // 整数（文本输入 + 后端校验）
	KindBool   FieldKind = "bool"   // 复选框
	KindSelect FieldKind = "select" // 下拉框（Options 给候选值）
	KindList   FieldKind = "list"   // 多行文本，一行一项（对应 []string）
	KindText   FieldKind = "text"   // 多行文本（普通字符串）
	KindSecret FieldKind = "secret" // 密码类：GUI 显示掩码，未修改则不提交
)

// ConfigField 是一个可在 GUI 表单里编辑的配置项（字段值来自当前生效配置）。
type ConfigField struct {
	// Path 是点分路径，与 config.yaml 的嵌套结构一致，例如 server.port、file.allowed_paths。
	Path string `json:"path"`
	// Label 是表单里显示的中文名；Group 是分组名（服务/日志/上传与文件…）。
	Label string `json:"label"`
	Group string `json:"group"`
	// Kind 决定渲染控件；Value 是当前值的文本形式（List 用 \n 连接，Secret 为掩码）。
	Kind  FieldKind `json:"kind"`
	Value string    `json:"value"`
	// Options 仅 KindSelect 有意义。
	Options []string `json:"options,omitempty"`
	// Description 是字段下方的一行说明。
	Description string `json:"description,omitempty"`
	// HotReload=true 表示保存后无需重启（后端 watcher 自动应用）。
	HotReload bool `json:"hot_reload"`
	// ReadOnly 表示 GUI 只展示、不提交。
	ReadOnly bool `json:"read_only,omitempty"`
}

// ConfigView 是配置表单的完整数据：当前配置文件路径 + 字段列表。
type ConfigView struct {
	Path   string        `json:"path"`
	Fields []ConfigField `json:"fields"`
}

// SaveFieldsResult 是保存表单字段的结果。
type SaveFieldsResult struct {
	// BackupPath 是保存前自动备份出来的 config.yaml.bak-时间戳。
	BackupPath string `json:"backup_path,omitempty"`
	// HotApplied 是本次改动里会立即生效的字段（无需重启）。
	HotApplied []string `json:"hot_applied,omitempty"`
	// NeedRestart 是本次改动里需要重启后端才生效的字段。
	NeedRestart []string `json:"need_restart,omitempty"`
}