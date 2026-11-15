# 桌面控制台（GUI）架构说明

> 2026-11-15 起，桌面控制台是**独立的 C++/Qt6 项目**，源码在仓库根目录 `server_ui/`，
> 不再放在 Go 后端模块里。后端 `new_server/` 只保留 headless 服务本体与本地管理接口。
> 构建/使用说明见 [server_ui/README.md](../server_ui/README.md)。
>
> 2026-11-15 晚补充：GUI ↔ 后端的控制通道已从 **TCP loopback HTTP** 改成
> **本机 IPC（Windows 命名管道 / Linux Unix socket）**。代理的 TUN/透明接管可能劫持 127.0.0.1
> 的流量，IPC 不经过 IP 栈，从根上避免这类问题；也不再占用端口。

## 1. 两个进程，一条窄契约

```
┌────────────────────┐        config.yaml（读写，热加载）        ┌────────────────────┐
│  syc-file-gui.exe  │ ───────────────────────────────────────► │  syc-file.exe      │
│  (Qt6 托盘控制台)  │ ◄────────── 日志文件（只读）──────────── │  (Go headless)     │
│  独立项目 server_ui│   本地 IPC：runtime.json 里的 ipc + token   │                    │
└────────────────────┘ ◄══════════════════════════════════════► └────────────────────┘
                          GET /status  POST /restart /shutdown
                          GET/POST /config/fields  POST /config/validate
```

- GUI 退出、崩溃都不影响后端；后端停掉 GUI 显示「未运行」，仍可看配置和日志历史。
- **后端启动后自动拉起控制台**（`gui.enabled`）：交互式启动直接起子进程；
  Windows 服务（session 0）用 `WTSQueryUserToken + CreateProcessAsUser` 放进当前登录用户的桌面。
- **控制台单独启动不会拉起后端**；控制台自己的「启动后端」按钮才会。
- 配置与日志走**文件**（GUI 直接读 `config.yaml` / `log/*.log`），只有「实时状态 + 动作 + 保存前校验」走
  **本地管理接口**。

## 2. 本地管理接口（`new_server/internal/localapi`）

后端启动时在本机 IPC 端点上起一个极小的 HTTP 服务，把连接信息写到
`config/runtime.json`（权限 0600，退出即删）：

```json
{"pid":1234,"ipc":"syc-file-localapi-1a2b3c4d5e6f7a8b","token":"32hex",
 "started_at":"...","version":"1.0.0","mode":"dev",
 "config_path":"...","working_dir":"..."}
```

`ipc` 的取值：

| 平台 | 端点 | 权限 |
|---|---|---|
| Windows | 命名管道名（如 `syc-file-localapi-1a2b...`，完整路径 `\\.\pipe\...`） | 管道 DACL：SYSTEM/Administrators 完全控制，Authenticated Users 读写 |
| Linux/macOS | `/tmp/syc-file-<uid>-<hex>.sock` | 0600（仅属主） |

所有请求必须带 `X-Syc-Local-Token: <token>`（常量时间比较，缺/错一律 401）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/status` | 运行状态快照（版本/pid/业务端口/DB/Redis/资源/路径…） |
| POST | `/restart` | 请求后端优雅退出（`ExitRestart`），由部署层决定是否拉起 |
| POST | `/shutdown` | 请求后端优雅退出（`ExitQuit`） |
| POST | `/config/validate` | 请求体是待保存的 YAML 原文；解析+校验通过回 200，否则 400 + `{"error":...}` |
| GET | `/config/fields` | 配置表单字段目录（见 3.1） |
| POST | `/config/fields` | 保存配置表单（见 3.1） |

安全边界：只在本机 IPC 上开放、每次启动随机端点 + 随机 token、只暴露上述能力，不承载业务。
**不要用 curl 调**（没有端口）；排障用命名管道客户端或 Go 测试：

```powershell
$j = Get-Content config\runtime.json -Raw | ConvertFrom-Json
$pipe = New-Object System.IO.Pipes.NamedPipeClientStream('.', $j.ipc, 'InOut')
$pipe.Connect(3000)
$req = "GET /status HTTP/1.0`r`nX-Syc-Local-Token: $($j.token)`r`nConnection: close`r`n`r`n"
$bytes = [Text.Encoding]::UTF8.GetBytes($req); $pipe.Write($bytes,0,$bytes.Length); $pipe.Flush()
(New-Object System.IO.StreamReader($pipe, [Text.Encoding]::UTF8)).ReadToEnd()
```

## 2.1 自启动、单实例与启停语义

- 后端 `Start()` 在 localapi 就绪后调用 `internal/desktoplaunch`：
  - 当前进程在 session 0（Windows 服务）→ `WTSGetActiveConsoleSessionId` + `WTSQueryUserToken`
    + `DuplicateTokenEx` + `CreateProcessAsUser`，把控制台放进当前登录用户的桌面（`winsta0\default`）；
    没有用户登录时只记一条 info「跳过自动拉起」；取令牌失败记 warn。
  - 交互式会话（用户手动启动后端）→ 直接 `CreateProcess`/`exec` 起子进程。
  - 不等待、不跟踪：控制台退出不影响后端，后端退出也不带走控制台。
- 控制台可以做**单实例**：Windows 用命名互斥体 `Local\syc-file-gui-<sha1(config.yaml 规范化路径)[:16]>`，
  Linux 用 QLocalServer 绑定同名 socket。后端重启时重复拉起的那次会自己退出（`--auto-start` 不弹窗）；
  用户手动再开一次会通过本地管道通知已有窗口「show」。
- 控制台**单独启动不会拉起后端**，只显示「未运行」；「启动后端」按钮才用 `CreateProcessW`
  （Linux `startDetached`）拉起后端，且后端起来后会再自动拉起控制台（被单实例挡掉，不会多开）。
- 停止/重启按钮走 IPC 的 `/shutdown`，等待条件为 `runtime.json` 删除或 IPC 端点关闭；
  服务进程权限不足时明确提示去「服务」里停止 `FileSyncBackend`。

## 3. 配置热加载

后端 `config.StartWatcher` 1 秒轮询 `config.yaml` mtime，变更后：

1. 备份 `config.yaml.bak-YYYYMMDD-HHMMSS`；
2. `config.Live()` 原子替换快照；
3. 热字段（日志级别/分片大小/限流等 16 项）立即生效，其余标注「需重启」。

因此 GUI 保存配置的流程是：编辑原文 → `POST /config/validate` → 备份 + 原子写文件 → 提示哪些即时生效。
GUI 自己也会标记哪些属于需重启字段（见 `server_ui` 的配置页提示文案）。

## 3.1 配置表单接口（GUI 的「表单」页签）

`server_ui` 的配置页有两个页签：**表单**（默认）和**原文编辑**（兜底）。表单数据来自后端：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/config/fields` | 返回 `{path, fields:[{path,label,group,kind,value,options,description,hot_reload,read_only}]}`；字段由后端反射 `config.Config` 生成（分组/中文标签/热标记来自后端维护的小表，不会与代码漂移） |
| POST | `/config/fields` | 请求体 `{"values":{"点分路径":"文本值"}}`，只提交改动字段；后端 `config.UpdateValues` 保留注释、备份、原子写并校验，返回 `{backup_path, hot_applied, need_restart}` |

密码类（`*password` / `*secret`）字段在表单里只显示掩码，留空表示不修改。
`[]struct` 这类无法安全文本化的配置（如 `supervisor.processes`）不会出现在表单里，需要时用「原文编辑」。

## 4. 目录与产物

| 位置 | 说明 |
|---|---|
| `server_ui/` | C++/Qt6 控制台源码（CMake 工程，独立构建） |
| `new_server/internal/localapi/` | 后端侧本地管理接口（命名管道 / Unix socket + HTTP 语义） |
| `new_server/internal/desktoplaunch/` | 后端启动后自动拉起控制台（Windows 服务落用户桌面） |
| `new_server/internal/desktopapi/` | 接口线上契约类型（纯标准库） |
| `E:\fileSynchronization\syc-file-win\syc-file-gui.exe` | 部署后的控制台（+ 同目录 Qt DLL/插件） |

部署脚本 `new_server/deploy-win.ps1` 会构建后端与 Qt 控制台并装到同一安装目录；
开始菜单快捷方式「私有云服务器设置」与 HKCU Run 自启项都指向 `syc-file-gui.exe`，
所以换实现后不需要重新注册。

## 5. 排障

- **托盘不出现 / 窗口不弹**：先看进程在不在（`Get-Process syc-file-gui`）；
  启动时读 `gui.start_minimized`，为 `true` 时只驻留托盘，双击托盘图标打开。
  服务启动早于用户登录时会跳过自启动，登录后由 HKCU Run 的快捷方式拉起。
- **概览显示未运行但服务在跑**：
  1. `runtime.json` 是否存在、是否有 `ipc` 字段（旧版本后端只有 `port`，不支持 IPC）；
  2. 普通用户能否打开对应命名管道（可用 `NamedPipeClientStream` 试；DACL 见第 2 节）；
  3. GUI 与服务是否指向同一份 config（同目录/同一 config 路径）。
- **服务启动时没有拉起控制台**：看后端日志里的「桌面控制台已自动拉起 / 跳过自动拉起 / 自动拉起失败」；
  失败常见原因是当前没有登录用户、`gui.ui_path` 路径不对、或服务账户缺少 SeTcbPrivilege（LocalSystem 有）。
- **保存配置被拒绝**：看 `/config/validate` 返回的 YAML 解析/校验错误；旧配置可用
  `config.yaml.bak-*` 回滚。