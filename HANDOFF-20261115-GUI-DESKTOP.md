# 交接：后端桌面 GUI（系统托盘控制台）

> 日期：2026-11-15 13:00（Asia/Shanghai）
> 状态：**设计已定、契约已落盘、依赖已加入；业务代码尚未开始。**
> 下一个会话请从 §9「下次会话第一步」继续。
>
> **2026-11-15 13:25 修订（重要）**：GUI 与后端确定是**两个独立进程**，
> §1/§4/§6 中「同一进程」「internal/desktop 同进程运行」的表述作废，以 §12 为准。

---

## 0. 一句话目标

给 Go 后端 `new_server/` 加一个**常驻系统托盘的 Go 原生 GUI**（Fyne），用于**管理后端配置、查看日志详情、看运行状态、重启后端**；Windows 可跑、Linux 可构建；用 build tag `desktop` 与服务器 headless 构建隔离，**不改变现有部署产物与行为**。

## 1. 用户需求与已拍板的决策

原始需求：给后端加个简单 GUI 页面，Linux 和 Windows 都能用，能简单管理后端配置、查看日志详情，平时在托盘里，点击托盘图标 GUI 出现。

用户已明确：

- **用 Go 写桌面 GUI，不要 Tauri**（"Tauri 太重"）；Rust egui 作为备选被否掉，倾向 Go。
- 访问范围：**仅本机**（不需要远程管理/账号鉴权）。
- 配置保存后：**尽量热重载**，做不到的字段必须明确标"需重启"。
- 采用方案：**Fyne v2.8.2**（纯 Go 桌面框架，自带系统托盘）；GUI 与后端跑在**同一进程**（`syc-file` 加 `-tags desktop` 构建即为桌面版），托盘/窗口生命周期由后端主进程管理。

## 2. 仓库勘察结论（已核实）

| 项 | 事实 |
|---|---|
| 后端 | `new_server/`，Go 1.25.3 + Gin + GORM；cgo（`pkg/sync_core` 静态链接 `sync_core/lib/libsync_core.a`，26.6 MB） |
| 入口 | `new_server/cmd/main.go`（315 行，全部初始化逻辑写在 main 里，需要重构成 `internal/app`） |
| 配置 | `new_server/config/config.go`（viper，`Init()` 只读一次），`config/config.yaml` 注释非常丰富（db/redis/log/whitelist/auth/server/file/user/share/quick_share/sync/supervisor/monitor/thumbnail/vpn） |
| 日志 | `pkg/logger`（zap + lumberjack）：`log/server.log` + 轮转备份 `log/server-<时间戳>.log`；托管子进程日志 `log/supervisor/*.log` |
| Windows 运行 | `deploy-win.ps1` 构建 `syc-file.exe` → 安装到 `E:\fileSynchronization\syc-file-win` → `Start-Process -WindowStyle Hidden` 启动（没有服务化、没有自动重启） |
| Linux 运行 | systemd（`install-systemd.sh`，`Restart=always`）或 Docker（`docker/supervisord.conf`，`autorestart=true`） |
| 已有管理 API | `/v1/admin/*`；其日志查询查的是**数据库操作日志**，不是运行日志 |
| 桌面客户端 | `filesync-desktop`（Tauri 2 + Vue3 + naive-ui）已有托盘和日志窗口 —— 那是**客户端**，本次不碰 |
| 可借鉴 | 仓库根 `.watchdog-close-tray/`（用户之前的 Rust+egui 托盘项目，`tray.rs`/`gui.rs`），**未跟踪，勿修改/勿提交** |
| 工具链 | Go 1.25.3 在 `C:\Program Files\Go\bin\go.exe`；mingw gcc；cargo 1.88；node/pnpm 也在 |
| VM | `ssh vm`（192.168.31.120）当次连不上（超时），Linux 实机验证需等环境恢复 |
| 分支/工作区 | 分支 `agent-dev`；工作区已有其他未提交改动（Android、filesync-desktop 等），**不要混入本次改动** |

## 3. 已完成的工作

1. **契约已落盘**：`new_server/internal/desktopapi/desktopapi.go`
   - 纯标准库、中文注释、无 GUI 依赖，headless 构建也会编译它。
   - 类型：`Status`、`ProcessStatus`、`KV`、`ConfigField`、`ConfigView`、`SaveResult`、`ApplyResult`、`LogFile`、`LogQuery`、`LogLine`、`LogBatch`。
   - 接口 `Backend`：`Status / LoadConfig / SaveConfigText / SaveConfigFields / ApplyConfig / ListLogs / ReadLog / ReadLogFrom / OpenPath / RestartBackend / Shutdown`。
   - **所有方法要求并发安全**（GUI 多 goroutine 调用）。
2. **依赖已加入**：`new_server/go.mod` 增加 `fyne.io/fyne/v2 v2.8.2`，`github.com/go-ole/go-ole` 由 1.2.6 升到 1.3.0，`go.sum` 增加 fyne 两条哈希。
   - ⚠️ Fyne 的子依赖（glfw、go-gl、go-text、godbus 等）**尚未**进入 go.sum；第一次 `go build -tags desktop` / `go mod tidy` 会补写 —— 属预期，先跑一次并提交这两个文件。
   - ⚠️ 当前 go.mod 里 fyne 标记为 `// indirect`，等 desktop 包 import 后 `go mod tidy` 会修正为直接依赖。
3. **未做**：没有创建 team task，没有 spawn 队友（`list_agents` 只有 lead）。业务代码、Fyne GUI、配置编辑、日志读取、app 重构、构建脚本、文档都还没开始。

## 4. 目标架构（已定稿）

```
new_server/
├─ cmd/main.go                 # 变薄：app.Init → app.Start → (desktop.Run 或信号等待) → app.Shutdown
├─ internal/app/               # 新增：启动逻辑从 main 搬过来
│   ├─ app.go                  #   App 结构体 / Init / Start / Shutdown
│   ├─ status.go               #   desktopapi.Status 采集
│   ├─ desktop_adapter.go      #   App 实现 desktopapi.Backend（配置/日志/重启适配）
├─ internal/desktopapi/        # 已存在：唯一契约
├─ internal/desktop/           # 新增：Fyne GUI + 托盘（build tag desktop；stub 无 tag）
├─ internal/logtail/           # 新增：日志文件发现/尾部读取/增量跟随/解析
├─ config/live.go              # 新增：原子 Live 快照
├─ config/editor.go            # 新增：yaml.Node 编辑（保留注释）、备份、校验
└─ pkg/logger/logger.go        # 改：AtomicLevel，支持热改日志级别
```

GUI 启动契约（`internal/desktop`）：

```go
//go:build desktop
func Available() bool
func Run(ctx context.Context, b desktopapi.Backend) (desktopapi.ExitReason, error) // 阻塞到用户退出/重启

//go:build !desktop
func Available() bool { return false }
func Run(...) (desktopapi.ExitReason, error) { return desktopapi.ExitQuit, nil }
```

`ExitReason`（`Quit` / `Restart`）定义在 `desktopapi`，由 `cmd/main.go` 决定随后执行 Shutdown 还是重启。

## 5. 热重载设计（用户明确要求）

- `config.Live()` 返回**原子快照**（`atomic.Pointer[Config]`），启动时 = `Conf`；所有"可热改"的读取点改为读 `Live()`。
- 保存流程：写文件（备份）→ `config.LoadFile` 校验 → `eff := Clone(Live())` → 对每个**热字段** `SetByPath(eff, key, 新值)` → `PublishLive(eff)` → `logger.SetLevel(eff.Log.Level)`。
- 冷字段（需重启）只写文件、不动 Live；`SaveResult.NeedRestart` 列给 GUI。
- **热字段清单（初稿，以代码审计为准）**：
  - `log.level`
  - `monitor.sys_detail_interval_seconds`、`monitor.process_top_n`
  - `file.upload.max_file_size`、`file.upload.max_filename_length`、`file.upload.allowed_extensions`、`file.upload.forbidden_extensions`
  - `file.allowed_paths`
  - `whitelist`
  - `auth.token_expire`、`auth.refresh_expire`
  - `share.max_expire_minutes`
  - `quick_share.default_expire_minutes`、`quick_share.max_capacity_bytes`、`quick_share.memory_threshold_bytes`
  - `user.max_size`
- **冷字段（必须重启）**：`server.port/mode`、`db.*`、`redis.*`、`log.path/format/console/file/max_*`、`file.storage.*`、`share.temp_path/clean_interval_minutes`、`quick_share.base_path`、`sync.*`、`thumbnail.*`、`supervisor.*`、`vpn.*`、`gui.*`、`server.name`（读起来是热字段，但保持简单可先冷/后热，做审计时确认）。
- 现有读取点约 121 处（`grep config.Conf.`），只迁移热字段对应的调用点，其余保持 `Conf`（启动后不再变）以避免数据竞争。

## 6. 工作流拆分（建议直接按此 spawn）

### W1 Lead（关键路径）：`internal/app` 重构 + 集成
- 写作用域：`new_server/cmd/main.go`、`new_server/internal/app/**`（新增）。
- 把 `cmd/main.go` 的初始化链（配置/日志/DB/迁移/bootstrap/token/Redis/ws/monitor/sync/thumb/路由/STUN/supervisor/http.Server）原样搬进 `app.Init/Start/Shutdown`，**行为必须与现在逐字一致**（注意 `defer logger.Sync`、`procpriority.Raise`、`SYC_SKIP_SUPERVISOR`、CORS、avatar 静态挂载、`handler.RegisterRouters`、`sv.Start()` 的位置）。
- `App` 实现 `desktopapi.Backend`：
  - `Status`：版本/模式/节点/PID/启动时间/端口/配置路径/日志目录/工作目录/DB/Redis 健康（短超时 Ping）/在线 WS 连接数（看 `internal/ws` 是否好拿，拿不到先 0）/supervisor 进程（目前 Supervisor 无状态查询接口，可先列配置项或给 `supervisor` 加 `Snapshot()`）/内存与 goroutine。
  - `LoadConfig`：读磁盘文件 + `config.LoadFile` 校验 + 表单字段（值来自**磁盘文件**，secret 掩码 `********`）。
  - `SaveConfigFields` / `SaveConfigText`：写文件（备份）→ 热应用 → 返回 `SaveResult`。
  - `ApplyConfig`：重新读文件 + 热应用。
  - `ListLogs/ReadLog/ReadLogFrom`：转发 `internal/logtail`。
  - `OpenPath`：Windows `explorer` / Linux `xdg-open`。
  - `RestartBackend`：只安排重启（不直接 `os.Exit`）。
  - `Shutdown`：取消主循环。
- `cmd/main.go` 新流程：`Init → Start →（gui 启用且 desktop.Available()：Run；否则：等 SIGINT/SIGTERM）→ Shutdown`。
- **重启实现**：
  - managed 环境（`INVOCATION_ID` 存在 或 `/.dockerenv` 存在）→ 直接优雅退出，返回 `RestartManaged`，由 systemd/supervisord 拉起。
  - 否则 spawn 新进程：env `SYC_WAIT_PID=<旧 pid>`，新进程启动最前面等旧 pid 消失再继续；Windows `SysProcAttr{CreationFlags: DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP}`，Linux `Setsid`。

### W2 config-dev：配置层（Live 快照 + 保留注释的编辑 + 热字段迁移）
- 写作用域：`new_server/config/`、`new_server/pkg/logger/`、`new_server/internal/middleware/auth.go`、`new_server/internal/monitor/sys_detail.go`、`new_server/internal/handler/file/**`。
- `config/live.go`：`Live()/PublishLive()/Clone()`。
- `config/editor.go`（基于 `gopkg.in/yaml.v3` 的 `yaml.Node`，保留 `HeadComment/LineComment`）：
  - `LoadFile(path) (*Config, error)`（复用 Init 的归一化：mode 默认/小写、vpn 默认值、`SYC_MODE` 覆盖）
  - `ReadText(path)` / `SaveText(path, text)`（备份 + 原子替换 + 权限 0600）
  - `UpdateValues(path string, values map[string]string) (backupPath string, err error)`（按点路径改节点，其余内容原样保留）
  - `Validate(*Config) error`
- `pkg/logger`：改 zap `AtomicLevel`，`SetLevel(string) error`；`IsLevel/IsDebug` 读原子级别。
- 迁移热字段读取点到 `config.Live()`（清单见 §5）。
- 输出最终热字段清单给 Lead（配置表单 schema 标注用）。
- 测试：注释/未知键保留、备份、非法 YAML 不落盘、Live 并发读写（`-race`）。

### W3 gui-dev：Fyne GUI + 托盘
- 写作用域：`new_server/internal/desktop/**`（新增）。
- 结构建议：`app.go`（生命周期/主线程）、`tray.go`、`window.go`、`page_overview.go`、`page_config.go`、`page_logs.go`、`stub.go`（`!desktop`）、`ui_test.go`（test driver）。
- 托盘：Fyne 自带 `desktop.App`（`a.(desktop.App).SetSystemTrayMenu / SetSystemTrayIcon`）。Windows 与 Linux 的 systray 实现均为纯 Go（见 §8）。
- 窗口：点 × 收起到托盘（`SetCloseIntercept(func(){ w.Hide() })`）；启动默认只驻留托盘（`gui.start_minimized`）。
- 托盘菜单：**打开控制台**、打开日志目录、打开配置文件、重启后端、退出。
- 页面：
  - 概览：Status 卡片 + 定时刷新（2s + 手动刷新），展示 §6-W1 的字段；按钮：打开配置、打开日志目录、重启、退出。
  - 配置：按 `Group` 分节，按 `Kind` 渲染控件；标注「即时生效 / 需重启」；保存后弹出 `SaveResult`（备份路径、Reloaded、NeedRestart）；另有「原文编辑」标签页（`ConfigView.Text`，保存走 `SaveConfigText`）和「重新加载」（`ApplyConfig`）。
  - 日志：`ListLogs` 选择文件（当前文件优先）；级别下拉 + 关键词 + 行数；等宽列表按级别着色；选中行看详情（时间/级别/caller/msg/Extra）；「实时跟随」开关（1s `ReadLogFrom(offset)`）；打开日志目录。
- 并发：Backend 调用放 goroutine，UI 更新用 `fyne.Do`。
- 中文渲染：Fyne 2.8 有系统字体回退（`internal/painter` 用 `fontscan.UseSystemFonts + lookupRuneFont`），**必须用 test driver 截图验证**（`test.NewApp()` + `window.Canvas().Capture()` 存 PNG，再用 read_image 看）；不行再注入字体（优先 `C:\Windows\Fonts\simhei.ttf`；`msyh.ttc` 是 TTC，Fyne 可能不支持）。
- 测试：`go test -tags desktop ./internal/desktop`，用 mock Backend + 渲染截图。

### W4 data-dev：日志读取包
- 写作用域：`new_server/internal/logtail/**`（新增）。
- API：
  - `List(root string) ([]desktopapi.LogFile, error)`：`server.log`、`server-*.log`（lumberjack 备份）、`supervisor/*.log`；`Current=server.log`；当前文件优先 + ModTime 倒序。
  - `Tail(root, path string, q LogQuery) (LogBatch, error)`：从文件尾部倒读；解析 zap JSON（`ts/level/caller/msg` → 结构化，其余字段拼 `Extra`）；解析失败 `Parsed=false`、`Message=Raw`；`MinLevel` 过滤；`Keyword` 大小写不敏感整行匹配；`Truncated` 标记。
  - `ReadFrom(root, path string, offset int64, max int) (LogBatch, error)`：增量读；文件变小 → `NextOffset=0` 并让客户端重载；只返回完整行；单次上限（行数/1MB）。
  - 路径必须落在 root 内（`filepath.Clean` + 前缀校验，拒绝 `..` 与绝对路径）。
- 测试：JSON/纯文本混排、轮转（rename+新建）、截断、过滤、越界路径拒绝。

### W5（可后置）：构建 / 部署 / 文档
- `deploy-win.ps1`：构建加 `-tags desktop`；新增 `-NoGui` 走原 headless；启动改为桌面版（可加 `-ldflags -H=windowsgui` 避免黑窗，代价是 stdout 不可见）。`run.ps1` / `cmd/dev` 支持 GUI 构建（devbuild 传 tags）。
- `config.example.yaml`（以及 deploy-win 生成配置）加：
  ```yaml
  gui:
    enabled: true          # 仅 -tags desktop 构建有效；headless 构建忽略
    start_minimized: true
  ```
- 新文档 `new_server/GUI.md`：构建（Windows/Linux 依赖）、使用、热/冷字段清单、安全说明、重启语义、排障。
- `README.md` / `ARCHITECTURE.md` 各补一段。

## 7. 环境与构建命令（下次直接抄）

```powershell
$env:Path = 'C:\Program Files\Go\bin;' + $env:Path
$env:CGO_ENABLED = '1'; $env:CC = 'gcc'
$env:GOPROXY = 'https://goproxy.cn,direct'; $env:GOSUMDB = 'off'
$env:GOMODCACHE = Join-Path $env:TEMP 'syc-go\mod'
$env:GOCACHE   = Join-Path $env:TEMP 'syc-go\cache'
cd E:\fileSynchronization\project\new_server

# 1) headless 构建（基线，必须始终通过）
go build -o "$env:TEMP\syc-file-test.exe" ./cmd

# 2) desktop 构建（首次会补 go.sum + 编译 Fyne，约 2~4 分钟）
go build -tags desktop -o "$env:TEMP\syc-file-desktop.exe" ./cmd

# 3) 单测
go test ./config/ ./internal/logtail/ ./internal/app/
go test -race ./config/ ./internal/logtail/

# 4) Fyne 界面渲染验证（截图到 $env:TEMP 后人工看）
go test -tags desktop ./internal/desktop
```

注意：`go build` 默认 `-mod=readonly`，若 go.mod/go.sum 不完整会失败；首次先 `go mod tidy`（会把 fyne 改成直接依赖并补全子依赖）或 `go build -mod=mod`。

## 8. 已验证的事实（别重复踩坑）

- Fyne v2.8.2 在本机 Windows + mingw cgo **可编译**（hello world 42 MB）。
- `fyne.io/systray`（fyne 依赖，v1.12.x）：**Windows 纯 Go**（`golang.org/x/sys/windows`）；**Linux 纯 Go**（`godbus` StatusNotifierItem），无 gtk/appindicator 的 cgo 依赖。
- Fyne 2.8 引入系统字体回退（`fontscan`），中文大概率可直接显示 —— 仍需截图确认。
- Fyne 依赖树较大（glfw、go-gl、go-text/render、go-ole、godbus 等）；Linux 构建需要 X11/GL 开发头文件（`xorg-dev`、`libgl1-mesa-dev`），**服务器 headless 构建不受影响**（没有 desktop tag 就不编译 Fyne）。
- 本机访问 `proxy.golang.org` 不通，必须 `GOPROXY=https://goproxy.cn,direct`。
- 旧 DSH 沙箱下 shell 不能写 workspace（只有 write/edit 工具能写）；**新会话 policy 已是 danger-full-access**，应可直接写，但仍建议优先 write/edit。
- VM `192.168.31.120` 当次 ssh 超时，Linux 真机验证待环境恢复。
- 不要动 `.watchdog-close-tray/`（未跟踪的另一个项目，仅作交互参考）。

## 9. 下次会话第一步

1. `get_goal` 拿 revision，然后 `update_goal` action=`resume` 重新武装目标（goal id：`goal-66116f31-14b9-4b32-b801-f984831cf9b4`）。
2. 先执行 `go mod tidy` + `go build -tags desktop ./cmd`（此时 desktop 包还不存在，先建一个空的 `internal/desktop` 带 tag/stub 文件），确认 Fyne 依赖树完整、两种构建都过。
3. 建 4 个 team task（写作用域互不重叠）：
   - W1 Lead：`cmd/main.go` + `internal/app/**`
   - W2 config-dev：`config/` + `pkg/logger/` + `internal/middleware/auth.go` + `internal/monitor/sys_detail.go` + `internal/handler/file/**`
   - W3 gui-dev：`internal/desktop/**`
   - W4 data-dev：`internal/logtail/**`
4. spawn 3 名 fresh 队友，prompt 直接引用本文档对应章节 + 让它们先读 `new_server/internal/desktopapi/desktopapi.go`。
5. 集成顺序：W2/W3/W4 并行 → Lead 的 app 适配器接上 → 构建/单测/截图验收 → W5 构建脚本与文档。

## 10. 关键设计细节（实现者必读）

- **配置表单值来源是磁盘文件**（`config.LoadFile`），不是 `Live()`；否则冷字段会显示旧值。secret 字段显示掩码 `********`，GUI 未修改就不下发；保存只传变更项。
- **保存结果**：对比旧 `Live()` 与新文件得到 `Reloaded`（热）与 `NeedRestart`（冷）。
- **配置编辑必须保留注释与未知键**（`yaml.Node`），保存前备份 `config.yaml.bak-YYYYMMDD-HHMMSS`，写临时文件后 rename 原子替换，权限保持 0600。
- **托盘单击行为**：Fyne 的托盘是菜单式；先实现「菜单第一项＝打开控制台」，再评估 `fyne.io/systray` 是否支持单击直接显示窗口（Windows 可双击；Linux DBus 点击行为受限）。验收先按菜单实现。
- **GUI 构建建议** `-ldflags -H=windowsgui` 去掉控制台黑窗（日志仍落文件，GUI 里能看）；调试期可保留 console 子系统。
- **单实例**：同一份配置只允许一个后端进程；重复启动会端口冲突 —— MVP 不做防护，写进文档即可。
- **GUI 在无显示环境**（systemd/docker）：`desktop.Run` 初始化失败要能 recover 并回落为 headless 等待信号，不能把服务搞崩。

## 11. 风险与待决

1. **中文渲染**：Fyne 默认字体不含 CJK；2.8 的 fontscan 回退需实测。不行就嵌入 OFL 中文字体（约 10 MB）或加载 `simhei.ttf`。
2. **Docker 构建影响**：`go.mod` 引入 Fyne 后，Dockerfile 里 `go mod download` 会多下载依赖（不编译，因为无 tag）；如 VM 网络受限，可把 Dockerfile 的 `GOPROXY` 参数改成国内代理。
3. **热重载范围**：以代码审计为准，拿不准的字段一律标"需重启"，不要为凑热重载引入数据竞争。
4. **Linux 实测**：VM 恢复后再验证 `-tags desktop` 构建（需要 X11/GL 头文件）。
5. 用户 Token 预算紧张，建议下次直接按 §6 并行分工，减少重复勘察。

---

附：契约文件位置 `new_server/internal/desktopapi/desktopapi.go`（约 380 行，中文注释，直接读它作为唯一接口依据）。

---

## 12. 修订：GUI 与后端是两个独立进程（2026-11-15 13:25）

用户原话：「GUI 本身是 Go 后端的衍生，改动可以热重载，不能的部分可以改动累计后一波重启，所以这相当于两个进程，通过 IPC 通信？」

结论与落地方式：

- **两个二进制、两个进程**：`syc-file`（后端本身，headless，零 Fyne 依赖，部署产物与行为不变）+ `syc-file-gui`（Fyne 控制台，`-tags desktop`）。GUI 退出不影响服务；服务没跑时 GUI 也能打开并把它拉起来。
- **不自己造 IPC 协议**：能用文件的全走文件——
  - 配置：GUI 直接读写 `config.yaml`（保留注释）；**后端 watch 文件**，热字段即时生效，冷字段攒着并在日志里提示，等一波重启统一生效。
  - 日志：GUI 直接读日志目录（同机同用户）。
  - 只有「动作 + 实时状态」需要跨进程：后端在 `127.0.0.1` 上开**本地管理接口**（随机端口 + 每次启动随机 token，写 `runtime.json` 权限 0600）。
- **进程归属**：GUI 负责启停/重启后端进程；托盘「退出」只退 GUI，不停服务。

### 12.1 目录（相对 `new_server/`）

```
cmd/main.go                 # 变薄：app.Init → app.Start → 等信号 / 被 localapi 通知退出 → app.Shutdown
cmd/gui/                    # 新增：GUI 入口（main_desktop.go //go:build desktop；main_stub.go //go:build !desktop）
internal/app/               # 新增：启动装配、状态采集（从 main.go 搬出，行为逐字一致）
internal/localapi/          # 新增：本地管理 HTTP + runtime.json（仅 127.0.0.1 + token）
internal/guibackend/        # 新增：GUI 侧 desktopapi.Backend 实现（config + logtail + localapi + spawn）
internal/desktopui/         # 新增：Fyne 托盘/窗口/三个页面（//go:build desktop + stub）
internal/logtail/           # 新增：日志发现/尾部/增量/解析
config/live.go|editor.go|hot.go|watch.go
```

### 12.2 desktopapi 契约（已按两进程更新，凡冲突以文件注释为准）

- `Status` 新增 `Running bool`：后端没跑时 GUI 仍要能显示路径类信息。
- `Backend` 新增 `Running() bool`、`StartBackend() error`、`StopBackend() error`；
  `RestartBackend()` = 停+起；`Shutdown()` 语义改为**退出控制台（不停服务）**。

### 12.3 localapi（W1，写作用域 `internal/localapi/**`）

- 监听 `127.0.0.1:0`（随机端口，只绑 loopback）。
- 启动生成 32 hex token；写 `runtime.json` 到 **config.yaml 同目录**，权限 0600，退出时删除：
  ```json
  {"pid":1234,"port":45678,"token":"<32hex>","started_at":"RFC3339","version":"1.3.1",
   "mode":"dev","config_path":"E:\\...\\config\\config.yaml","working_dir":"E:\\..."}
  ```
- 接口（全部要求请求头 `X-Syc-Local-Token`，不匹配返回 401）：
  - `GET  /status`   → `desktopapi.Status` 的 JSON
  - `POST /restart`  → 202，随后进程优雅退出（托管环境由 systemd/docker 拉起；Windows 由 GUI 拉起）
  - `POST /shutdown` → 202，随后进程优雅退出
- 后端始终启用（headless 也可用 curl 管理）；GUI 从 runtime.json 拿 port/token。

### 12.4 config 包 API（W2 必须实现，签名可微调但语义不变）

```go
// live.go
func Live() *Config
func PublishLive(c *Config)
func Clone(c *Config) *Config
// editor.go（yaml.Node，保留注释/未知键；备份 config.yaml.bak-YYYYMMDD-HHMMSS；0600；原子替换）
func LoadFile(path string) (*Config, error)   // 归一化与 Init 一致（mode/vpn/SYC_MODE）
func LoadText(text string) (*Config, error)
func ReadText(path string) (string, error)
func SaveText(path, text string) (backup string, err error)
func UpdateValues(path string, values map[string]string) (backup string, err error)
func Validate(c *Config) error
// hot.go（静态分类，GUI 与后端共用；GUI 不需要后端 Live 状态也能算）
func HotPaths() []string
func IsHot(path string) bool
func ClassifyChanges(oldC, newC *Config) (reloaded, needRestart []string)
func ApplyHotInto(dst, next *Config) (reloaded []string)   // 只搬热字段
// watch.go（后端用；fsnotify 或 1s 轮询 mtime，防抖）
func StartWatcher(ctx context.Context, path string) (stop func(), err error)
//  变化 → LoadFile 校验 → 失败记 error 日志不落 Live → 成功则 ApplyHotInto(Live(), new) + PublishLive + logger.SetLevel
```

### 12.5 logtail API（W4）

同 §6-W4，包路径 `internal/logtail`，返回 `desktopapi.LogFile/LogQuery/LogBatch/LogLine`。

### 12.6 guibackend（W1 集成，写作用域 `internal/guibackend/**`）

实现 `desktopapi.Backend`：
`LoadConfig/SaveConfigText/SaveConfigFields/ApplyConfig` → config 包；`ListLogs/ReadLog/ReadLogFrom` → logtail；
`Status/Running/StopBackend/RestartBackend` → localapi（HTTP client）；`StartBackend` → spawn `gui.backend_path`；
`OpenPath` → explorer/xdg-open；`Shutdown` → 取消 GUI 主循环。
**离线行为**：后端未跑时 `Running()=false`，`Status()` 返回从 config 文件推导的路径 + 零值运行信息，`LoadConfig` 照常可用。

### 12.7 config.yaml 新增 `gui` 段（后端忽略未知键，仅 GUI 读；W5 写进示例与文档）

```yaml
gui:
  enabled: true          # 仅 syc-file-gui 使用
  start_minimized: true  # 启动只驻留托盘
  backend_path: ""       # 空 = GUI 同目录的 syc-file(.exe)
  backend_workdir: ""    # 空 = backend_path 所在目录
```

### 12.8 任务与写作用域（替换 §6 中 W1/W3）

| 任务 | 写作用域 |
|---|---|
| W1 Lead | `cmd/main.go`、`internal/app/**`、`internal/localapi/**`、`internal/desktopapi/**`、`internal/guibackend/**` |
| W2 config-dev | `config/**`、`pkg/logger/**`、`internal/middleware/auth.go`、`internal/monitor/sys_detail.go`、`internal/handler/file/**` |
| W3 gui-dev | `internal/desktopui/**`、`cmd/gui/**` |
| W4 data-dev | `internal/logtail/**` |
| W5 后置 | `deploy-win.ps1`、`config.example.yaml`、`GUI.md`、`README.md`、`ARCHITECTURE.md` |

### 12.9 验收（全部必须实测）

1. `go build ./cmd`（headless）过；`go build ./...` 过（gui 无 tag 时走 stub）。
2. `go build -tags desktop ./cmd/gui` 出 GUI exe；Fyne 依赖树进 `go.sum`；`go mod tidy` 后 fyne 变直接依赖。
3. 后端启动后 `runtime.json` 0600 且含 port/token；`curl -H 'X-Syc-Local-Token: …' 127.0.0.1:<port>/status` 返回 JSON。
4. 改 `log.level` 保存 → 后端即时换级别；改 `server.port` → GUI 标「需重启」，重启后生效；配置注释保留、备份文件生成。
5. Fyne 截图：中文正常、托盘菜单可用、三个页面可用（读图确认）。
6. 后端未运行时打开 GUI → 概览显示「未运行」，点「启动后端」能拉起。

### 12.10 本轮完成状态（2026-11-15 15:05）

**代码全部落地（未 commit，工作区即成果）**

| 工作流 | 状态 | 产出 |
|---|---|---|
| W1 | 完成 | `internal/app`（Init/Start/Stop/Status）、`cmd/main.go` 薄壳、`internal/localapi`（127.0.0.1 + 32hex token + runtime.json 0600）、`internal/guibackend`、`internal/desktopapi` 扩充（Running/Start/Stop/ExitReason）、`internal/supervisor/snapshot.go`、补迁 hot 读取点（storage/thumb/update/userLogin/vpn/updateUserInfo） |
| W2 | 完成 | `config/live.go|editor.go|hot.go|watch.go`、`pkg/logger` AtomicLevel、16 个热字段读取点迁移 |
| W3 | 完成 | `internal/desktopui`（托盘 + 概览/配置/日志）、`cmd/gui`（desktop/stub）、mock + 截图测试 |
| W4 | 完成 | `internal/logtail`（List/Tail/ReadFrom + 路径安全 + 85.9% 覆盖） |
| W5 | 完成 | `deploy-win.ps1`（`-NoGui`、GUI 构建、自动写 gui 段）、`config.example.yaml`、`GUI.md`、README/ARCHITECTURE、`.gitignore`（GUI 产物 + runtime.json 不入库） |

**已实测通过**

- `go build ./...`、`go build -tags desktop ./cmd/gui`、`go vet ./...`、`go mod tidy`（fyne 已转直接依赖）、新文件 gofmt 干净。
- `go test ./...`：除 `pkg/response.TestMigratedHandlersUseEnvelope`（`internal/admin/vps.go` 既有问题，与本次无关）外全绿；
  `-race` 覆盖 config/localapi/guibackend/logtail；`-tags desktop` 覆盖 desktopui（6 张截图）与 cmd/gui。
- GUI 真实冒烟：窗口模式、start_minimized 托盘模式各存活 8s，stderr 干净；截图人工读图确认中文正常、三页面可用。
- localapi 单测：token 缺失/错误 401、runtime.json 权限与 pid、退出删除、非法 runtime 拒绝。

**真实后端 E2E（2026-11-15 15:00，通过）**

本机 MySQL 起初起不来（datadir 里 `syncfile-dev` 与 ted/pc_info 的旧 `.ibd` 出现重复 tablespace ID）。
把冲突的 `syncfile@002ddev` 目录移出 datadir 后 MySQL 恢复（暂存 `E:\BtSoft\mysql\removed-dbs-20261115`），
然后用部署配置的副本（只改端口 18991）跑真实后端：

- `/status`：`version=1.0.0 port=18991 pid=49624 dbOK=true redisOK=true ws=0`
- `runtime.json`：pid 与进程一致、port 随机、token 32 hex，无 token 请求返回 401
- 热重载：把 `log.level` 改成 warn，后端日志出现热重载记录
- `/restart`：202 → 进程 15s 内优雅退出且 `runtime.json` 被删除

**GUI 真实联调（2026-11-15 15:20-15:25，通过）**

用隔离配置（端口 18991、`gui.*` 指向构建产物、独立日志目录）同时跑真实后端 + GUI，并用 Win32 截图/鼠标事件实测：

- 概览页：`运行中`、版本 1.0.0、PID 412、端口 18991、`数据库 正常`、`Redis 正常`、内存/goroutine —— 全部来自真实 localapi；截图 `%TEMP%\syc-gui-live.png`
- 配置页：读到真实 `config.yaml`（模式 dev、端口 18991、节点名 win-home、日志级别 debug…），热/冷角标正确；截图 `%TEMP%\syc-gui-live-config.png`
- 日志页：真实 `server.log` 500 行，时间/级别/caller/msg、级别过滤、实时跟随开关；截图 `%TEMP%\syc-gui-live-logs3.png`
- **面板按钮重启**：点「重启后端」→ 确认弹窗 → 是 → 后端 pid `412 → 50064`、`runtime.json` 同步换成新 pid/新端口，GUI 自动重连并显示新 PID；截图 `%TEMP%\syc-gui-after-restart.png`

**deploy-win.ps1 隔离安装验证（2026-11-15 15:27，通过）**

`-InstallDir %TEMP%\syc-win-test -Port 18992 -ConfigPath <已部署配置> -SkipTools -NoSupervisor -Force`：

- sys_core 构建（release 复用缓存）→ `syc-file.exe`（161.8MB）+ `syc-file-gui.exe`（46.9MB）构建成功
- 生成配置自动带上 `gui:` 段（enabled/start_minimized/backend_path/backend_workdir）
- redis 检查、端口检查、启动后端、`/v1/ping` 200 全通过；验证后已清理临时安装目录

**正式部署到 Windows 服务（2026-11-15 15:32-15:35，完成）**

- 服务 `FileSyncBackend`（nssm 包装、LocalSystem、Automatic）的二进制已替换为新构建：
  旧后端备份为 `E:\fileSynchronization\syc-file-win\syc-file.exe.bak-20261115-153248`，
  新 `syc-file.exe` 与构建产物 SHA256 一致，`syc-file-gui.exe` 同目录安装。
- nssm 退出策略：`AppExit` 子键 `(default)=Restart`、`0=Exit` —— 崩溃会自动重启，
  正常退出（GUI「停止后端」/服务停止）不会被拉起。
- 部署配置追加 `gui:` 段（`start_minimized: true`）；`runtime.json` 写在 `config\` 下，
  普通用户可读（GUI 用它的 pid/port/token 连 localapi，已验证）。
- 开机自启（注册表）：`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的
  `FileSyncBackendConsole = "...\syc-file-gui.exe"`，登录后 GUI 自动驻留托盘。
- 开始菜单：`%APPDATA%\Microsoft\Windows\Start Menu\Programs\FileSync 后端控制台.lnk`
  → 双击即开控制台（GUI 已退出时用它重开；无需去安装目录找 exe）。
- 实测：通过快捷方式启动 GUI（最小化托盘、无窗口）、概览显示服务后端
  `运行中 / PID 52040 / 端口 8991 / DB 正常 / Redis 正常`；截图 `%TEMP%\syc-gui-service-live.png`。

**仍待人工/环境确认**

- Linux 桌面（X11/GL）未实机验证。
- 托盘菜单的人工点击未做（按钮功能已实测）。
- 手动停止后端后：服务状态为 Stopped，可用 GUI 概览「启动后端」拉起（普通用户进程），
  或重启机器由服务自动恢复；如果要「停服务后一键以服务方式重启」，可再加一个提权快捷方式。

**下一步建议**：① 用户确认后一次性提交（不要把 `.watchdog-close-tray/`、`HANDOFF-*.md`、构建产物带进去）；② 旧二进制备份确认无问题后可删。

---

## 13. UI 反馈修订（2026-11-15 15:45-16:05）

用户反馈：灰色太浅 / 日志页控件太窄 / 是否按相对宽度布局 / 配置页滚动遇到输入控件很难滚、速度不跟手 / 字体怪。逐条处理：

1. **主题（`internal/desktopui/theme.go` 新增）**：自定义 `consoleTheme` 包装默认浅色主题，固定浅色变体：
   - 前景 `#1b1f24`、灰色（LowImportance 用的 `disabled`）`#4d565f`、占位符 `#666f78`；
   - 页面背景压成 `#ebebf2`、输入框纯白、分隔线 `#c4ccd6` —— 灰字在白底上不再发飘。
2. **字体**：优先加载 `C:\Windows\Fonts\Deng.ttf` + `Dengb.ttf`（等线，TTF 且中英字形一致），
   其次 `simhei.ttf`；加载失败或非 Windows 回退 Fyne 默认（Linux 仍是系统回退）。
   等宽场景（日志列表/原文）保留系统等宽字体。
3. **日志页工具栏按相对宽度重排**：文件选择框伸缩铺满整行；关键词输入框吃掉剩余宽度；
   级别/行数下拉给固定宽度（132/96），不再被 HBox 压成一条缝。
4. **配置页滚动**：
   - 根因：多行输入框自身带滚动区，光标移到上面时滚轮被它吃掉（Fyne 只把滚轮交给光标下第一个可滚动对象），整页滚不动；
   - 改法：列表/多行字段（allowed_paths、allowed_extensions、whitelist…）改成「预览 + 编辑…」，
     真正编辑放到独立弹窗（弹窗自己滚）。配置表单里不再有内嵌滚动控件。
5. **滚动速度**：新增 `scroll.go` 的 `fasterScroll`（内嵌 `*container.Scroll`，只重写 `Scrolled` 放大 4 倍增量），
   用在概览/配置/日志详情三处页面滚动。实测 3 格滚轮 ≈ 400px（原来 14 格才 ~230px）。
6. **验证**：`go vet -tags desktop`、`gofmt`、`go test -tags desktop ./internal/desktopui` 全绿；
   实机截图确认新主题/字体/工具栏/列表字段预览与编辑弹窗；已替换安装目录 `syc-file-gui.exe`
   并以 `start_minimized: true` 重启到托盘（后端服务未重启）。

备注：滚动倍率、配色都集中在 `scroll.go` / `theme.go` 顶部常量里，用户还想更快/更暗可再调。

---

## 14. 黑框与托盘消失修复（2026-11-15 16:10）

用户反馈「有个黑框挂着，关掉黑框托盘图标也消失」。定位与处理：

1. **根因**：上一轮为改编辑弹窗临时重编 `syc-file-gui.exe` 时**漏了 `-ldflags "-H=windowsgui"`**，
   装进安装目录的是控制台子系统（PE subsystem=3）版本 —— 黑框就是这个 GUI 自己的控制台宿主，
   关掉黑框 = 杀掉 GUI 进程，托盘图标自然一起消失。
2. **修复**：用 `-H=windowsgui` 重新构建（subsystem=2），替换安装目录二进制；
   旧的控制台版备份已删除，GUI 通过开始菜单快捷方式重启到托盘，验证：
   - 安装后 exe subsystem=GUI；
   - GUI 进程无 `conhost.exe` 子进程（无黑框）；
   - 3s 后进程存活、服务 `FileSyncBackend` Running、后端 `/v1/ping` 200。
3. **顺带加固**：`internal/guibackend/process_windows.go` 新增 `hideWindow()`，
   GUI 里「启动后端」用 `CREATE_NO_WINDOW + HideWindow` 拉起后端 —— 否则 GUI 无控制台时
   启动控制台子程序 `syc-file.exe` 会再弹一个黑框（而且关掉它会连带杀掉后端）。

教训：桌面版 GUI 的统一构建命令必须带 `-ldflags "-H=windowsgui"`，已在 `deploy-win.ps1` 里固化。

---

## 15. 滚动条回归与回滚（2026-11-15 16:25）

用户反馈「整个滚动条都不跟着动了」。定位：§13 加的 `fasterScroll`（内嵌 `*container.Scroll` 只重写
`Scrolled` 放大 4 倍）有致命缺陷 —— 内嵌后 Fyne 里有**两个 Widget 身份**：真正参与显示的是外层包装
对象的渲染器，而 `Scrolled` 转给内层 `Scroll` 后刷新的是内层对象的渲染器，于是画面/滚动条不更新。

处理：

1. **整体回滚自定义滚动**：删除 `scroll.go`，概览/配置/日志详情恢复 `container.NewVScroll`（原生滚动条、
   拖拽、惯性全部正常）。
2. **保留**「配置表单不含自带滚动控件」的修复（列表字段预览 + 编辑弹窗），滚轮在任意控件上都能滚整页；
   同时留了回归测试 `TestConfigFormHasNoScrollableControls`（递归检查控件树里没有 `fyne.Scrollable`）。
3. **速度限制**：Fyne 的滚轮速度是框架硬编码（Windows `scrollSpeed=25`，无公开倍率接口）。
   要更快只能自研滚动容器（自绘滚动条 + 拖拽 + 惯性），成本/风险较高，等用户决定是否值得做。
4. 重新构建（subsystem=GUI）并安装、重启托盘；`go vet`/`gofmt`/`go test -tags desktop` 全绿。

用户同时明确要求：不要再做屏幕级鼠标/截图操作来验证交互，交互效果由用户实际反馈。

---

## 16. GUI 迁移为独立 C++/Qt6 项目（2026-11-15 16:35-17:25，用户决策）

用户对 Fyne 的观感与「GUI 和后端混在一个模块」都不满意，决策：
**GUI 换成 C++/Qt6，独立成项目，从后端模块彻底移出。**

### 16.1 后端清理（`new_server/`）

- 删除：`cmd/gui/`、`internal/desktopui/`、`internal/guibackend/`、`internal/logtail/`（Fyne 相关全部移除）。
- `internal/desktopapi/` 只保留 localapi 回包的线上契约类型（Status/KV/ProcessStatus/ExitReason）。
- `internal/localapi/` 去掉客户端代码，只留服务端；新增 `POST /config/validate`（请求体=YAML 原文，
  解析+校验通过回 200，否则 400+error），`Handlers` 增加 `ValidateConfig`，`App` 实现之。
- `go mod tidy` 后 go.mod 不再含 Fyne；后端仍是单二进制 `syc-file.exe`。
- `deploy-win.ps1`：`-NoGui` 改为 `-SkipGui`，新增 `-QtDir`（默认 `C:\Qt\6.9.3\mingw_64`），
  构建段改成 cmake+ninja 构建 `server_ui/` + windeployqt 打运行库 + 拷贝到安装目录；
  拷贝前先杀掉托盘进程释放 exe。

### 16.2 新工程 `server_ui/`（C++/Qt6，CMake）

- 文件：`main.cpp`（入口/--config/启动时最小化）、`mainwindow.*`（外壳：页签+工具栏+托盘+轮询调度）、
  `overviewpage.*`、`configpage.*`、`logpage.*`、`backendclient.*`、`configfile.*`、`logline.*`，
  单文件均 < 500 行。
- 通信：只读写 `config.yaml`、`log/*.log`、`config/runtime.json` + localapi；不 import 后端 Go 代码。
- 功能对齐旧版：托盘常驻/双击开窗、概览（运行信息/依赖/路径，2s 刷新）、配置（原文编辑 + 保存前
  `/config/validate` + 备份 + 原子写）、日志（文件/级别/关键词/行数 + 1s 实时跟随 + 着色 + 打开目录）、
  启动（QProcess + CREATE_NO_WINDOW 静默、不弹黑框）/重启/停止后端。
- 产物仍叫 `syc-file-gui.exe`：开始菜单快捷方式与 HKCU Run 自启项无需重注册。

### 16.3 工具链与构建

- 用 `aqtinstall`（pip 装到 Python 3.11）下载 `Qt 6.9.3 win64_mingw` + `tools_mingw1310` + `tools_ninja`
  到 `C:\Qt`（免 Qt 账号）。
- 构建命令见 `server_ui/README.md`；本机已验证 Release 构建通过，产物 `build/syc-file-gui.exe`（GUI 子系统）。
- `windeployqt --release --no-translations --compiler-runtime`，去掉 `opengl32sw.dll`/`D3Dcompiler_47.dll` 后
  运行库约 35MB。

### 16.4 部署与验证（Windows）

- 后端：重建（含 /config/validate）并以 UAC 提权替换服务二进制，
  备份 `syc-file.exe.bak-20261115-171305`，服务 `FileSyncBackend` Running；runtime.json：
  pid 50780 / port 5902；`/status` 200（db_ok=true, redis_ok=true）；`/config/validate` 对当前配置回 ok、
  对非法 YAML 回 400。
- GUI：Qt 构建产物 + 运行库拷到 `E:\fileSynchronization\syc-file-win\`，托盘启动成功（启动即最小化）、
  无 conhost 子进程（不会再出现黑框）；进程存活、服务与后端 ping 正常。
- 冒烟：`QT_QPA_PLATFORM=offscreen` + `start_minimized=false` 的临时配置跑 6s，进程稳定（窗口/三页签构造无崩溃）。
- `go vet ./...`、`go test ./config ./internal/localapi`、`gofmt` 全绿。

### 16.5 仍待确认

- 用户实际点一遍新 UI（外观/交互验收）。
- Linux 版 Qt 控制台未构建（本机只装了 Windows Qt；需要时 apt 装 qt6-base-dev 后同一份 CMake 工程可编）。
- `deploy-win.ps1` 的 Qt 构建段逻辑已改并通过语法解析，但未从零完整跑一遍（本次是手工按同样步骤部署）。
- 回滚：旧二进制仍在安装目录 `*.bak-*`；旧 Fyne 代码可通过 git 恢复（未提交，但工作区删除，如需回滚从 HEAD 取）。
---

## 17. Qt GUI 第二轮：配置表单 + 日志显示修复（2026-11-15 17:30-18:05）

用户反馈两点：① 日志页级别附近有乱码；② 配置页不该直接丢 YAML 原文，应该是可填写表单（Go 旧版的做法对，只是不好看）。

### 17.1 日志乱码

根因是 HTML 组装顺序错了：`logpage` 先把空格替换成 `&nbsp;` 再 `escapeHtml`，`&` 被二次转义成
`&amp;nbsp;`，界面显示成字面量 `&nbsp;`（夹在时间/级别之间，看起来像乱码）。
改为先转义再加 `&nbsp;`（`server_ui/src/logpage.cpp`）。

### 17.2 配置表单（后端 + Qt 两侧）

后端（`new_server/`）：

- `internal/desktopapi` 增加配置表单契约：`FieldKind`（string/int/bool/select/list/text/secret）、
  `ConfigField`、`ConfigView`、`SaveFieldsResult`。
- `internal/localapi` 新增 `GET /config/fields`（字段目录+当前值）与 `POST /config/fields`
  （`{"values":{path:value}}`，保存改动字段）；`Handlers` 增加 `ConfigFields` / `SaveConfigFields`。
- `internal/app/config_fields.go` 新增：反射遍历 `config.Config` 生成字段表（点分路径、按分组排序、
  `[]string`→多行列表、`[]struct` 跳过、密码字段掩码、热标记取 `config.IsHot`），配一张人工维护的
  分组/中文标签/说明/下拉候选小表；保存走 `config.UpdateValues`（保注释 + 备份 + 原子写 + 校验），
  返回 `backup_path` 与 `hot_applied` / `need_restart` 分类。
- 单测：`internal/app/config_fields_test.go`（分组/类型/热/掩码/列表拼接/跳过 supervisor.processes）、
  `internal/localapi` 接口测试补 `/config/fields` GET/POST 与未知字段 400。

Qt（`server_ui/`）：

- `configpage` 重写为两个页签：**表单**（默认）+ **原文编辑**（兜底）。表单按后端字段渲染
  QLineEdit / QComboBox / QCheckBox / QPlainTextEdit，字段名旁标「即时生效/需重启」，字段下方显示说明；
  只提交改动过的字段，密码留空不提交；列表类多行框装了 wheel 事件过滤器，滚轮会转给页面滚动区。
- `backendclient` 增加 `fetchConfigFields` / `saveConfigFields` 与对应信号；保存后展示备份路径和
  热/冷字段清单，1.2s 后自动重拉字段（等后端 watcher 应用热字段）。
- 保存成功后弹窗，`MainWindow` 刷新概览状态。

### 17.3 部署与验证

- 后端重建（含新接口）并以 UAC 提权替换服务二进制：备份 `syc-file.exe.bak-20261115-175612`，
  服务 Running、`/v1/ping` 200；`/config/fields` 返回 72 个字段（15 个分组），未知字段 POST 回 400。
- Qt GUI 重建并替换安装目录 exe；托盘启动、无 conhost；`QT_QPA_PLATFORM=offscreen` +
  `start_minimized=false` 冒烟 8s 通过（配置表单真实拉取 72 字段并构建，无崩溃）。
- `go vet ./...`、`go test ./internal/localapi ./internal/app`、`gofmt` 全绿。
- 文档：`new_server/GUI.md` 增加 `/config/fields` 契约说明；`server_ui/README.md` 同步。

---

## 18. 视觉重构 + 连接修复（2026-11-15 19:00-19:30）

用户反馈：① 页面很素；② 没有和后端正确连接（概览一直「未运行」）；③ 日志处别处还有乱码。
本轮把这三件事都修完，并把界面整体重做。后端 Go 代码未改动。

### 18.1 连不上后端：三个叠加根因

1. **Qt 系统代理劫持 loopback**（主因）：本机开着 Clash（`127.0.0.1:7897`），
   `QNetworkAccessManager` 默认用系统代理，发往 `127.0.0.1:<localapi>` 的请求全被转给代理而失败。
   修复：`network_.setProxy(QNetworkProxy(QNetworkProxy::NoProxy))`。
2. **进程存活判断不可用**：原用 `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` 查 runtime.json 的 pid，
   后端以 Windows 服务（LocalSystem）运行时普通用户拿不到句柄（错误 5），永远判成「已退出」；
   改 Toolhelp/EnumProcesses 后，在 DSH 沙箱里跑 Qt 进程只能枚举到 64 个进程（普通控制台程序能枚举 549 个），
   仍不可靠。**最终方案：不再枚举进程**——「是否在运行」= localapi `/status` 是否回 200；
   启动前用 TCP 端口探测（localapi 端口 / `server.port`）防止重复拉起。
3. **QProcess 析构杀子进程**：原「启动后端」用 `QProcess::start` 后 `deleteLater()`，
   实测 `~QProcess` 会终止仍在运行的子进程（后端刚拉起就消失）。改为 Win32 `CreateProcessW` +
   `CREATE_NO_WINDOW`（Linux `QProcess::startDetached`）的脱离式启动，GUI 退出不会带走后端。

配套：`BackendStatus` 增加 `runtimePresent` / `detail`，概览页直接显示失败原因
（找不到 runtime.json / 连接被拒绝 / HTTP 状态码 / token 不匹配）；
停止/重启的等待条件改为「runtime.json 删除或 localapi 端口关闭」，权限不足时明确提示去服务里停。

### 18.2 乱码

- 日志页级别下拉的「全部级别」用 `QString::fromLatin1(const char *utf8)` 构造，UTF-8 被当 Latin-1 解释，
  显示成 `åð…` 乱码。级别筛选本轮改成胶囊按钮（全部/DEBUG/INFO/WARN/ERROR），并彻底移除该常量。
- 日志时间列一直显示 `—`：后端 zap 用 ISO8601 字符串（`2026-11-15T18:53:34.499+0800`），
  原代码只按秒级浮点数解析。现在两种都认（含 `+0800` 偏移归一化）。
- 纯文本行（ddns-go/cloudflared 等托管进程日志）不再硬塞「时间/级别/调用位置」占位列，直接按原文显示。

### 18.3 界面重构

- 新增设计系统：`theme.*`（Fusion + 全局 QSS + 调色板 + 微软雅黑 9.75pt + 复选框/下拉箭头自绘）、
  `icons.*`（Segoe Fluent/MDL2 图标 + 品牌徽标）、`widgets.*`（Card / StatCard / Pill / StatusOrb / 滚动页）。
- 外壳改为**左侧深色侧边栏 + 顶栏（标题/页副标题/状态胶囊/刷新/启动/重启/停止）+ 页面栈 + 状态栏**；
  托盘图标带状态小圆点（绿/红/琥珀）。
- 概览：状态大卡（自绘指示灯 + 一句话 + 刷新时间）、四张指标卡（已运行/端口/内存/依赖）、
  运行信息（两列）、依赖与资源（数据库/Redis 胶囊 + 托管进程两列）、路径信息（复制/打开按钮）。
- 配置：顶部路径卡 + 「表单 / 原文编辑」页签；表单按分组卡片渲染，左侧中文名 + 即时生效/需重启徽标，
  右侧控件 + 说明；保留列表字段编辑框的滚轮转发与只提交改动项的逻辑。
- 日志：工具栏两行（文件/刷新/打开目录/计数/实时跟随；级别胶囊/关键词/查询/行数）、
  深色日志面板（等宽、级别着色、列对齐、关键词高亮、深色滚动条）、行数上限真正生效、加载后贴底。
- 文件拆分保持单一职责，最大文件 `theme.cpp` 651 行，其余均 < 650 行。
- 新增开发自检工具 `server_ui_snapshot.exe`（`tests/snapshot.cpp`）：`--config/--page/--out/--width/--height`
  离屏渲染 PNG，用于检查布局/中文/在线离线两种状态。

### 18.4 验证

- `cmake --build` 通过（`syc-file-gui.exe` + `server_ui_snapshot.exe`），无警告级错误。
- 截图自检（1200×780）：
  - 在线概览：运行中 / v1.0.0 / pid 6468 / 端口 8991 / 数据库 Redis 正常 / 6 个托管进程；
  - 在线配置：72 个字段、分组卡片、徽标、说明；
  - 在线日志：`server.log` 500 行，时间列完整、级别着色、中文正常；
  - 离线（临时 config，无 runtime.json）：概览显示「找不到 …runtime.json」诊断，日志页仍可读历史日志。
- 连接根因在独立的 Qt 控制台探针中复现并验证修复（修复前 0 次成功，修复后 `/status` 200）。
- 脱离式启动用等价 Win32 小程序验证：子进程在启动器退出后仍存活。

### 18.5 部署

- 旧 `syc-file-gui.exe` 备份为 `.bak-<时间戳>`，替换为本次构建（Qt 运行库未变化，沿用安装目录现有 DLL）。
- 通过 `explorer.exe` 拉起新控制台（脱离当前会话的作业对象），`gui.start_minimized: true` → 驻留托盘。
- 后端服务 `FileSyncBackend` 未重启，`/v1/ping` 仍 200。

### 18.6 仍待人工确认

- 托盘单击/双击、按钮交互的实机手感（本轮不做屏幕级鼠标操作验证）。
- Linux 版未构建（本机只有 Windows Qt）。
- 普通用户停掉服务后由 GUI 拉起后端时，进程身份从 LocalSystem 变为当前用户（设计如此，但需要用户知晓）。

---

## 19. 控制通道改 IPC + 后端自动拉起 UI（2026-11-15 20:00-20:30）

用户要求：① 后端启动后自动拉起 UI；② UI 退出不退出后端；③ UI 可以单独停止/启动后端；
④ UI 单独启动不代表后端启动；⑤ HTTP 可能被代理 TUN 强行接管，通信协议改成 IPC。

### 19.1 后端：localapi 从 TCP loopback 改为本机 IPC

- `internal/localapi` 新增 `endpoint.go` + `listen_windows.go` + `listen_unix.go`：
  - Windows：命名管道 `\\.\pipe\syc-file-localapi-<16hex>`（每次启动随机），DACL
    `D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)`——SYSTEM/管理员完全控制，已认证用户读写；
  - Linux/macOS：`/tmp/syc-file-<uid>-<16hex>.sock`，权限 0600；
  - HTTP 语义不变，`http.Server` 直接跑在 `net.Listener`（管道/socket）上，所有 handler 不动。
- `runtime.json` 的 `port` 字段换成 `ipc`（Windows 管道名 / Unix socket 路径）；
  `Server.Port()` → `Server.Endpoint()`。
- 依赖新增 `github.com/Microsoft/go-winio v0.6.2`（纯 Go，命名管道 listener/dialer；`go mod tidy` 后为直接依赖）。
- 测试改为「HTTP over IPC」：`testHTTPClient` 用自定义 DialContext 连 `dialLocal(endpoint)`；
  `go test ./internal/localapi` 三个用例全部通过。
- 原有 token 鉴权保留（DEFENSE IN DEPTH）；代理/防火墙不再有可劫持的 IP 路径，也不占端口。

### 19.2 后端：启动后自动拉起控制台

- `config.GuiConfig` 正式进 `config.Config`（此前 gui 段是“未知键”）：
  `enabled`、`ui_path`（后端读取）+ `start_minimized`/`backend_path`/`backend_workdir`（GUI 读取）。
  控制台表单里会多出「桌面控制台」分组（需重启）。
- 新增 `internal/desktoplaunch`：
  - 交互式会话：`exec.Command` + `DETACHED_PROCESS`（Windows）/ `Setsid`（Linux）拉起
    `syc-file-gui.exe --config <config> --auto-start`；
  - Windows 服务（session 0）：`WTSGetActiveConsoleSessionId` → `WTSQueryUserToken` →
    `DuplicateTokenEx` → `CreateEnvironmentBlock` → `CreateProcessAsUser`
    （desktop `winsta0\default`），把 UI 放进登录用户桌面；没有用户登录/取不到令牌只记日志，不影响后端。
  - 拉起失败/跳过都不会影响后端启动。
- `app.Start()` 在 localapi 就绪后调用它；UI 退出不影响后端（子进程完全脱离）。

### 19.3 控制台：IPC 客户端 + 单实例 + 启动语义

- 新增 `server_ui/src/ipcclient.*`：`QLocalSocket` 上的极简 HTTP/1.0 客户端
  （`Connection: close`，读到 EOF 解析状态行/头/body；超时 4s；`probe()` 做快速可达性判断）。
- `backendclient.*` 全面改用 IPC：runtime.json 解析 `ipc`，`/status`、`/restart`、`/shutdown`、
  `/config/fields`、`/config/validate` 全部走 `IpcHttpClient`；移除 QNetworkAccessManager / 端口探测。
  「是否在跑」= `/status` 200；启动前先探 IPC 端点，连得上就拒绝重复拉起。
- 单实例：`syc-file-gui-<sha1(规范化 config 路径)[:16]>`
  - Windows 用命名互斥体 `Local\...` 做权威判定（实测 QLocalServer 的命名管道允许多实例，单靠
    `listen()` 失败会起出第二个窗口），管道仅用于“通知已有窗口 show”；
  - Linux 用 QLocalServer 绑定同名 socket；
  - `--auto-start`：已有实例就静默退出（后端重启不会弹窗）；手动启动则通知已有窗口显示。
- UI 单独启动只创建窗口 + 轮询，**没有任何拉起后端的代码路径**；「启动后端」按钮才
  `CreateProcessW` 拉起后端（后端起来后又会自动拉起 UI，被单实例挡掉）。
- 入口新增 `--auto-start`，UI 的 Hero 文案改为 `HTTP <port>`（业务端口，不是控制通道）。

### 19.4 配置 / 部署

- `config.example.yaml`、`deploy-win.ps1`（含老配置补 `ui_path`）、GUI.md、server_ui/README.md 同步。
- 部署实机（2026-11-15 20:16）：
  - 服务 `FileSyncBackend` 停止 → `syc-file.exe` 备份 `syc-file.exe.bak-20261115-2016xx` → 替换为新构建
    （sha256 `E30AA373…`）→ 启动；
  - 安装目录 `config.yaml` 补 `gui.ui_path: ""`；GUI 替换为新构建；
  - 新 backend pid 53700，`runtime.json` 带 `ipc: syc-file-localapi-85502aea7dd1ede8`；
  - **普通用户**用 NamedPipeClientStream 调 `/status` 得到 200（db/redis true），确认 SYSTEM 创建的管道
    DACL 对桌面用户可用；
  - 服务自动拉起的 GUI：pid 34960，session 1，owner `DESKTOP-FOS49RO\sunyuanling`——
    session 0 → 用户桌面的 `CreateProcessAsUser` 路径实测通过；
  - 单实例实测：同 config 起第二个/第三个实例均退出，只保留一个进程；
  - GUI 通过 IPC 连上服务后端（离屏截图 `build/shots/e2e-overview.png` 显示运行中/pid 19452，
    是隔离测试后端）；
  - **真实桌面 GUI 复核**：Explorer 拉起 `syc-file-gui.exe`（pid 49080，session 1，用户 sunyuanling），
    用 UI Automation 读到窗口文本：「● 运行中 / 后端运行中 / HTTP 8991 · PID 53700 / 数据库、Redis 正常」，
    证明普通桌面进程通过命名管道连 SYSTEM 服务后端正常。
- 注意：DSH 沙箱里直接启动的窗口型进程会拿到受限令牌，`server_ui_snapshot.exe` 在沙箱内连
  SYSTEM 创建的管道会报 Access denied（同一 exe 用 Explorer/cmd start 在桌面侧运行则正常）；
  这只影响自检工具，不影响产品。截图证据请用隔离测试后端那组，或直接看桌面上的控制台。

### 19.5 遗留 / 注意

- 控制台用户需要能读 `config/runtime.json`；管道本身已允许已认证用户，但如果部署把 config 目录
  收紧到 SYSTEM/管理员，GUI 会显示「找不到 runtime.json」。
- Linux 未实机验证（Unix socket 路径 + Qt QLocalSocket 逻辑已按同一套写）。
- 服务启动早于用户登录时没有桌面，自动拉起会跳过（登录后 HKCU Run 兜底）。
- 后端二进制替换仍需要管理员权限（本次用 UAC 提权脚本完成）。
- nssm 的 `AppExit Default=Restart`、`AppExit 0=Exit`：GUI 发 `/shutdown` 后端以 0 退出时
  服务不会被自动拉起（符合「UI 单独停止」预期）；GUI 的「启动后端」会以当前用户身份拉起
  `syc-file.exe`，此时服务仍是停止状态，身份从 LocalSystem 变为当前用户。

---

## 20. 应用图标与名称（2026-11-15 21:05-21:20）

用户要求：① 应用图标换成 `E:\fileSynchronization\icon\云端同步设置图标.png`；
② 应用名称改为「私有云服务器设置」。

### 20.1 图标

- 源图 1254×1254 RGBA，先按 alpha 裁掉透明边（bbox 99,116-1184,1164），留 4% 边距再缩放到
  **512×512** 存 `server_ui/resources/appicon.png`；同一内容生成多尺寸
  `appicon.ico`（16/24/32/48/64/128/256）。
- **exe 图标 + 版本信息**：`resources/app.rc.in` → 构建时 `configure_file` 成 `build/app.rc`
  （`IDI_ICON1 ICON` 用绝对路径，避免 windres 相对路径问题；`#pragma code_page(65001)`
  保证中文版本信息正确）。`FILEVERSION 0.1.0`，`FileDescription/ProductName = 私有云服务器设置`。
  CMake 把 `build/app.rc` 只加进主程序（截图工具不需要）。
- **Qt 侧统一图标**：`resources/resources.qrc` 把 512 PNG 编成 `:/app/icon.png`；
  `icons::appIcon(px, withStatusDot, running, known)` 负责缩放（2x DPR 清晰），
  托盘图标在右下角叠状态点（绿=运行、红=未运行、琥珀=未知），资源缺失时退回旧的 FS 蓝色徽标。
  窗口图标、侧边栏 logo、托盘图标、`QApplication::setWindowIcon` 全部走它。
- 实机验证：`Get-Item syc-file-gui.exe).VersionInfo` 的 FileDescription/ProductName 均为
  「私有云服务器设置」；`Icon::ExtractAssociatedIcon` 提取出的 32×32 图标与源图一致。

### 20.2 名称

- 新增 `src/appinfo.h`：`displayName()=私有云服务器设置`、`subtitle()=服务端控制台`、
  `vendorName()=FileSync`。窗口标题、托盘 tooltip/通知标题、侧边栏品牌、`QApplication` 的
  applicationName/DisplayName、版本信息、快捷方式全部引用它，避免多处手写漂移。
- 侧边栏品牌改为图标 + 「私有云服务器设置 / 服务端控制台」，`BrandTitle` 由 16px 调到 15px
  以容纳 8 个汉字（1200×780 截图确认不截断）。
- **产物文件名仍是 `syc-file-gui.exe`**：服务自动拉起、`gui.ui_path` 默认值、快捷方式、
  IPC 单实例约定都以它为准，改文件名收益低、破坏面大；对外显示名已全部改掉。
- 开始菜单快捷方式：`%APPDATA%\...\Programs\私有云服务器设置.lnk`（旧
  `FileSync 后端控制台.lnk` 已删），Target=安装目录 `syc-file-gui.exe`，IconLocation 指向 exe。
- HKCU Run 自启项：`FileSyncBackendConsole` → `私有云服务器设置`，值仍为带引号的 exe 路径。
- 版本信息 `CompanyName` 保留 `FileSync`，`OriginalFilename` 仍为 `syc-file-gui.exe`。

### 20.3 验证

- 构建通过；`build/syc-file-gui.exe` 版本信息/图标正确。
- 部署：停旧 GUI → 备份旧 exe（`.bak-<时间>`）→ 替换 → Explorer 拉起（pid 43640）。
- UI Automation 读窗口：标题 **私有云服务器设置**，概览显示「● 运行中 /
  HTTP 8991 · PID 53700 / 节点 win-home」；单实例仍只保留一个进程。
- 截图 `server_ui/build/shots/rename-overview.png`（沙箱内连服务管道仍受限，只用于看布局）。

---

## 21. 单文件安装包（MSI，2026-11-15 22:00-22:45）

用户要求：构建产物应该是一个单文件；运行后走安装流程，指定安装路径并释放依赖文件。

### 21.1 先试 NSIS：能装文件，但注册表/快捷方式被火绒拦截

- 用 NSIS 3.11（Tauri 缓存里的 `makensis`）做了自解压 setup exe（10.6 MB，中文 MUI 向导、
  可选目录、开始菜单、HKCU 自启、卸载清单），编译和文件释放都正常；
- 本机 **火绒（HipsDaemon）** 会拦截未签名安装包写注册表/开始菜单：
  NSIS 里 `WriteRegStr`、`CreateDirectory "$SMPROGRAMS"`、`CreateShortcut` 全部返回错误
  （`IfErrors` 实测 `write-error / dir-error / shortcut-error`），`ExecWait powershell` 也被拦
  （helper 脚本根本没执行）；文件释放不受影响。这解释了“装完没有快捷方式/自启”的现象。
- 结论：不是脚本写错，换安装器。

### 21.2 改用 Windows Installer（MSI，WiX 5）

- `msiexec` 是系统信任的安装服务，火绒放行：实测 HKCU Run、开始菜单快捷方式、
  「应用和功能」卸载项、文件释放全部成功。
- 新增 `server_ui/installer/make-msi.ps1`：
  - 按 `dist` 目录递归生成 WiX v5 工程：每个子目录（platforms/styles/imageformats/…）
    单独建 `<Directory>` + `<ComponentGroup>`，保证 Qt 插件落在正确子目录；
  - 中文安装向导：`WixUI_InstallDir` + `-culture zh-CN`，许可页 RTF 由脚本生成
    （中文转 `\uN?` 转义），目录页可自选安装路径；
  - per-user：`Scope="perUser"` + `ALLUSERS=2` + `MSIINSTALLPERUSER=1`，
    默认装到 `%LOCALAPPDATA%\Programs\私有云服务器设置`，不需要管理员；
  - 开始菜单快捷方式、HKCU 登录自启、ARP 图标/版本、固定 UpgradeCode 支持升级；
  - `.wixpdb`/生成的 `app.wxs` 放在 `build\installer\wix-build`，`release\` 只留 MSI。
- `build-win.ps1 -Installer`（隐含 `-Package`）调用它，输出
  `server_ui\release\私有云服务器设置-0.1.0.msi`（12.9 MB，单文件）。
- 前置：WiX 5（`dotnet tool install --global wix --version 5.*`）+
  `wix extension add -g WixToolset.UI.wixext/5.0.2`；WiX v6/v7 需要接受 OSMF EULA，脚本用 v5。

### 21.3 实测

- `msiexec /i msi /qn INSTALLFOLDER=<dir>`：20 个文件（含 `platforms\qwindows.dll` 等子目录）、
  开始菜单快捷方式、HKCU Run、ARP 全部就位，exit 0；
- `msiexec /x msi /qn`：本包文件/快捷方式/Run/ARP 清除，预置的 `keep.txt` 与
  `config\config.yaml` 保留（不会误删同目录后端文件）；
- 向导 UI 标题「私有云服务器设置 安装程序」，中文，目录页可选路径；
- 说明：ARP 实际注册在 HKLM（Windows Installer 行为），仍可在「应用和功能」卸载；
  per-user 安装不需要管理员。
- 旧的 NSIS 脚本/产物已删除，`release\` 与 `dist\` 都在 `.gitignore` 里。

---

## 22. 安装到别处后找不到后端：运行时定位 + 安装器外部传 -config（2026-11-15 22:55-23:30）

用户把控制台装到 `E:\Program Files\私有云` 后，界面仍默认读
`<安装目录>\config\config.yaml`，报“后端未运行/找不到 runtime.json”。用户意见：
“可以用 -config 指定，为什么不把它放在 GUI 外面指定？”

### 22.1 控制台：运行时定位后端（不写死安装位置）

- 新增 `src/configlocator.{h,cpp}`：没有显式 `--config` 时按顺序定位：
  1. `<程序目录>\config\config.yaml`（同目录部署，兼容旧行为）；
  2. Windows 服务注册：`FileSyncBackend` 的 `Parameters\AppDirectory` /
     `Parameters\Application` / `Parameters\AppParameters`（nssm 包装的真实后端）以及
     `ImagePath`（直接注册 exe 的服务），覆盖 `syc-file` 等常见别名；
  3. 正在运行的 `syc-file.exe` 进程（Toolhelp + `QueryFullProcessImageName`，用可执行文件目录定位）；
  4. Linux/macOS：扫 `/proc/<pid>/exe|cwd|cmdline`；
  5. 都没有 → 回落 `<程序目录>\config\config.yaml`，界面明确显示“配置文件不存在”。
- 显式 `--config` 仍然优先；但**显式路径不存在时自动回退到运行时发现**
  （快捷方式/安装器里的路径可能因后端搬家而失效），避免卡死在不存在的路径。
- 配置页路径显示“（不存在）”并给出 `--config` 提示；不再只有一句“读取配置字段失败”。

### 22.2 安装器：把 -config 写在 GUI 外面

- MSI 在安装时通过 RegistrySearch 读 `FileSyncBackend\Parameters\AppDirectory`，
  `SetProperty` 生成 `BACKENDCONFIG=<后端目录>\config\config.yaml` 和 `BACKEND_ARGS=--config "..."`，
  写进**开始菜单快捷方式参数**和 **HKCU Run 命令**——用户不需要在控制台里填任何路径。
- 也支持外部覆盖：`msiexec /i 私有云服务器设置.msi BACKENDCONFIG=<config.yaml>`。
- 版本升到 **0.1.1**（exe 版本资源/ MSI 同步）。

### 22.3 实测

- 安装到 `E:\Program Files\私有云` 后：快捷方式参数
  `--config "E:\fileSynchronization\syc-file-win\config\config.yaml"`，HKCU Run 同样带该参数；
  启动后 UIA 读到「● 运行中 / HTTP 8991 · PID 53700」。
- 不带 `--config` 直接启动安装目录的 exe：同样通过运行时定位连上服务后端。
- 用 `--config Z:\nope\config.yaml` 启动：自动回退到服务注册的配置（配置页显示正确路径）。
- 安装/卸载/保留同目录后端文件仍正常。

### 22.4 事故与恢复（重要）

- 测试 0.1.1 MSI 的 MajorUpgrade 时，把用户实机已安装的 0.1.0 产品卸载掉了
  （文件被删、ARP 消失）；随后用 0.1.1 MSI 重新安装到 `E:\Program Files\私有云`，
  文件/快捷方式（带 `--config`）/HKCU Run/ARP 全部恢复并验证运行中。
- 教训：同一 UpgradeCode 的 MSI 测试安装会升级/卸载实机产品；以后这类验证用不同
  UpgradeCode 的测试包或虚拟机，不要在本机实装目录上跑。