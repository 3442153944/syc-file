# server_ui — 私有云服务器设置（FileSync 桌面控制台，C++/Qt6）

独立的桌面控制台工程（对外名称「私有云服务器设置」）：常驻系统托盘，查看后端运行状态、
编辑配置（热字段即时生效）、过滤/跟随日志、启停与重启后端。

与 Go 后端**完全解耦**：不 import 后端代码，只通过

- `config/config.yaml`（读写；表单保存走后端 localapi，原文保存前经 `/config/validate` 校验）
- `log/*.log`（只读，zap JSON 行 + 托管子进程纯文本行）
- `config/runtime.json` + **本机 IPC**（Windows 命名管道 / Linux Unix socket）上的管理接口：
  `GET /status`、`POST /restart`、`POST /shutdown`、`GET/POST /config/fields`、`POST /config/validate`

通信。设计说明见 [../new_server/GUI.md](../new_server/GUI.md)。

> 控制通道**不走 TCP**：代理的 TUN/透明接管可能劫持发往 127.0.0.1 的流量；命名管道 / Unix socket
> 不经过 IP 栈，代理无从接管，也不占端口。

## 目录 / 职责

```
CMakeLists.txt
resources/
  appicon.png        应用图标（512px，源自 icon/云端同步设置图标.png）
  appicon.ico        exe 图标（16/24/32/48/64/128/256 多尺寸）
  resources.qrc      Qt 资源：:/app/icon.png（窗口/侧边栏/托盘统一用它）
  app.rc.in          Windows 图标 + 版本信息（名称「私有云服务器设置」），构建时生成 app.rc
src/
  appinfo.h          应用名称等对外文案（窗口/托盘/版本信息保持一处）
  main.cpp           入口：解析 --config / --auto-start、单实例、应用主题、创建窗口/托盘
  theme.*            设计系统：Fusion 风格 + 全局 QSS、调色板、字体、复选框/下拉箭头自绘
  icons.*            Segoe Fluent/MDL2 线性图标 + 应用图标（缺字体时退回系统图标）
  widgets.*          通用积木：Card / StatCard / Pill / StatusOrb / 滚动页
  ipcclient.*        本机 IPC 上的 HTTP 客户端（QLocalSocket，请求 HTTP/1.0 + Connection: close）
  backendclient.*    与后端通信：runtime.json(ipc/token)、/status、/restart、/shutdown、/config/*、
                     脱离式拉起后端（Windows CreateProcess；Linux startDetached）
  configpage.*       配置页：表单（字段由 /config/fields 提供，只提交改动）+ 原文编辑兜底
  configfile.*       配置文件读/备份/原子写
  logline.*          日志行解析（zap JSON / 纯文本）、尾部读取、文件发现
  logpage.*          日志页：文件/级别胶囊/关键词/行数 + 实时跟随 + 深色日志面板
  mainwindow.*       外壳：侧边栏导航 + 顶栏（标题/状态/操作）+ 页面栈 + 托盘 + 状态轮询
  overviewpage.*     概览页：状态大卡 / 四张指标卡 / 运行信息 / 依赖与托管进程 / 路径
tests/
  snapshot.cpp       离屏截图工具（开发自检用，不进安装包）
```

## 通信与故障语义（重要）

- **控制通道只走本机 IPC**：`runtime.json` 里的 `ipc`（Windows 命名管道名 / Linux socket 路径）
  + `token`。GUI 用 `QLocalSocket` 连接，在字节流上发 HTTP/1.0 请求。不占用端口，也不经过
  代理/虚拟网卡——代理的 TUN 模式无法接管命名管道或 Unix socket。
- **「是否在运行」以 IPC 端点是否可达为准**，不枚举进程（Windows 服务以 LocalSystem 运行时，
  普通用户拿不到进程句柄；沙箱环境还会过滤进程列表）。`/status` 回 200 才算运行中；
  有 `runtime.json` 但端点连不上/返回异常时，概览会显示具体原因（连接失败 / HTTP 状态码 / token 不匹配）。
- **启动后端前先探 IPC 端点**：还连得上就拒绝再拉起一个实例，避免双进程。
- **运行时信息以 `runtime.json` 为准**：backend 启动时原子写入、退出时删除；残留旧文件不会被误判成“在跑”。
- **停止/重启的等待条件是 runtime.json 删除或 IPC 端点关闭**；服务进程权限不足时会提示去
  「服务」里停 `FileSyncBackend`，不假装成功。
- **单实例**：同一份 `config.yaml` 只允许一个控制台。Windows 用命名互斥体判定，Linux 用
  QLocalServer 绑定；后端重启时重复拉起的那次会自己退出（`--auto-start` 不弹窗），
  用户手动再开一次会通知已有窗口显示。
- **自启动方向是单向的**：后端启动 → 自动拉起控制台；控制台启动 → **不会**拉起后端。

## 构建

### Windows（MinGW + Qt6）

前置：Qt 6.9+（MinGW 64 位 kit）+ CMake 3.21+ + Ninja。脚本会自动在 `C:\Qt` 下找最高版本的
`mingw_64`，并定位同一套 Qt 里的 MinGW / Ninja（也可用 `-QtDir` 显式指定）。

```powershell
# 普通构建 -> server_ui\build\syc-file-gui.exe
powershell -ExecutionPolicy Bypass -File server_ui\build-win.ps1

# 指定 kit / 清理重建 / 把 Qt 运行库打进 dist\（可整目录拷走运行）
powershell -ExecutionPolicy Bypass -File server_ui\build-win.ps1 `
  -QtDir C:\Qt\6.9.3\mingw_64 -Clean -Package

# 单文件安装包（安装向导可选路径，msiexec 释放 exe + Qt 依赖）
powershell -ExecutionPolicy Bypass -File server_ui\build-win.ps1 -Installer
# -> server_ui\release\私有云服务器设置-<版本>.msi
```

- 产物：`server_ui\build\syc-file-gui.exe`（GUI 子系统，无控制台窗口）、
  `server_ui\build\server_ui_snapshot.exe`（离屏截图工具）；
- `-Package` 额外产出 `server_ui\dist\`：exe + windeployqt 打好的 Qt DLL/插件（调试/绿色分发用）；
- `-Installer`（隐含 `-Package`）产出**单个 MSI**；需要 WiX 5，首次执行：

  ```powershell
  dotnet tool install --global wix --version 5.*
  wix extension add -g WixToolset.UI.wixext/5.0.2
  ```

### Linux（系统 Qt6）

依赖：`cmake`、`g++`（C++20）、Qt6 开发包（Core/Gui/Widgets/Network，如 `qt6-base-dev`）、可选 `ninja`。

```bash
./server_ui/build-linux.sh                  # -> server_ui/build/syc-file-gui
./server_ui/build-linux.sh --clean -j8
QT_PREFIX=/opt/Qt/6.9.3/gcc_64 ./server_ui/build-linux.sh --prefix /opt/syc-file-gui
```

Linux 没有 exe 资源/版本信息，图标来自 Qt 资源；运行时用系统 Qt6，单实例与本地管理接口走 Unix socket。

<details>
<summary>手动 CMake（其他平台 / 排障用）</summary>

```powershell
$env:Path = 'C:\Qt\6.9.3\mingw_64\bin;C:\Qt\Tools\mingw1310_64\bin;C:\Qt\Tools\Ninja;' + $env:Path
cmake -S server_ui -B server_ui/build -G Ninja -DCMAKE_BUILD_TYPE=Release `
  -DCMAKE_PREFIX_PATH=C:/Qt/6.9.3/mingw_64 `
  -DCMAKE_CXX_COMPILER=C:/Qt/Tools/mingw1310_64/bin/g++.exe `
  -DCMAKE_C_COMPILER=C:/Qt/Tools/mingw1310_64/bin/gcc.exe `
  -DCMAKE_MAKE_PROGRAM=C:/Qt/Tools/Ninja/ninja.exe
cmake --build server_ui/build
```

</details>

## 开发自检：离屏截图

```powershell
# 概览 / 配置 / 日志 三个页面各渲染一张 PNG（窗口不会真的弹到屏幕上）
& server_ui/build/server_ui_snapshot.exe --config <config.yaml> --page 0 --out overview.png
& server_ui/build/server_ui_snapshot.exe --config <config.yaml> --page 1 --out config.png
& server_ui/build/server_ui_snapshot.exe --config <config.yaml> --page 2 --out logs.png
```

- `--page`：0 概览 / 1 配置 / 2 日志；`--width/--height` 可调窗口尺寸（默认 1200×780）。
- 等 2.2 秒（一次状态轮询 + 配置字段拉取）后再截图；在线/离线状态都会真实反映。
- 用一张临时 config（例如 `log.path` 指向测试目录、目录里没有 runtime.json）可以验证离线外观。
- 截图工具**不启用单实例**，可以在已有一个控制台运行时单独渲染。

## 打包 / 部署

### 单文件安装包（MSI）

```powershell
powershell -ExecutionPolicy Bypass -File server_ui\build-win.ps1 -Installer
```

输出 `server_ui\release\私有云服务器设置-<版本>.msi`（约 13 MB，单个文件）：

- 双击进入**中文 Windows Installer 向导**（欢迎 → 许可 → **选择安装路径** → 安装 → 完成）；
- 安装时由 `msiexec` 释放 `syc-file-gui.exe` + Qt DLL/插件（含 platforms/styles/… 子目录），
  创建开始菜单快捷方式和 HKCU 登录自启项，并在「应用和功能」注册卸载入口；
- 默认装到 `%LOCALAPPDATA%\Programs\私有云服务器设置`（当前用户，无需管理员）；
- 卸载只删除安装包释放的文件，同目录的后端/配置/日志不会被删。

> 为什么用 MSI 而不是自解压 exe：本机火绒等 HIPS 会拦截未签名自解压安装包写注册表/开始菜单
> （NSIS 实测 `WriteRegStr`/`CreateShortcut` 全部失败），而 `msiexec` 是系统信任的安装服务，实测正常。

### 绿色部署

- `-Package` 产出 `server_ui\dist\`（Qt 运行库齐全，可整目录拷走；配置用 `--config` 指定）；
- 日常部署：`new_server/deploy-win.ps1` 会构建后端 + Qt 控制台并安装到
  `E:\fileSynchronization\syc-file-win`（`-SkipGui` 跳过 GUI，`-QtDir` 指定 Qt 目录）。

## 运行

```
syc-file-gui.exe [--config <path>] [--auto-start]
```

- 默认配置路径：`<程序目录>\config\config.yaml`；
- `gui.start_minimized: true` 时只驻留托盘，双击托盘图标开窗；
- **后端启动后会自动拉起控制台**（`gui.enabled: true`，后端 `internal/desktoplaunch` 负责；
  Windows 服务会把它放进当前登录用户的桌面）。控制台单独启动**不会**拉起后端；
- `--auto-start` 是后端拉起时使用的标记：已有实例就静默退出，不弹窗；
  手动启动（不带该参数）时如果已有实例，会让已有窗口显示出来；
- 关窗口 = 隐藏到托盘，真正退出用托盘菜单「退出控制台」；
- 托盘退出不会停后端；「启动/停止/重启后端」只操作后端进程本身。

## 已知限制

- 启动非服务方式的后端时，进程以当前用户身份运行；如果后端原本是 Windows 服务（LocalSystem），
  停掉服务后由 GUI 拉起会变成普通用户进程（权限、工作目录随之变化）。
- 服务方式运行时「启动/停止/重启」依赖 IPC 优雅退出；权限不足时强杀会失败并给出提示。
- 托盘菜单的单击/双击行为受 Windows 托盘实现限制，双击打开控制台。
- Windows 服务在用户登录前启动时没有桌面可放控制台，会跳过自启动（登录后 HKCU Run 兜底）。
- 控制台用户必须能读 `config/runtime.json`（命名管道 DACL 已允许 Authenticated Users）；
  如果 config 目录被收紧到只有 SYSTEM/Administrators 可读，概览会显示「找不到 runtime.json」。