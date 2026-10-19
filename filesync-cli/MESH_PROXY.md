# 本机与局域网组网代理

桌面「组网」页和 `syc proxy` 共用 `mesh-proxy/` Rust 库。先建立 WireGuard 隧道，再启动代理。
代理出站绑定指定隧道的本机 `172.1.*` 地址，仅允许访问 `172.1.0.0/16`；域名在代理端解析，所有解析结果都必须位于该网段。

## 桌面端

在「组网」页启动隧道，然后在「本机 / 局域网代理」中选择端口并启动。
默认混合端口 `7897`，HTTP、HTTPS CONNECT 和 SOCKS5 TCP 共用同一监听。
默认只监听 `127.0.0.1`；开启「允许局域网连接」时监听 `0.0.0.0`，「需要密码」为独立开关，默认关闭，本机和局域网均可免密码使用。
仅开启「需要密码」时填写用户名和密码；开启后本机与局域网请求均须认证。密码不写入配置文件或 localStorage，启动成功后清空页面密码框。

页面通过 Tauri 事件显示活跃连接、接入/失败计数和转发字节数。停止代理关闭监听及已有连接；停止隧道、退出登录也会停止桌面代理。
关闭主窗口保留托盘运行时，代理继续工作；重新打开页面可查询实际状态。重启软件后须重新开启代理。

## CLI（Windows / Linux）

```powershell
# 已由桌面端或 CLI 建立 syc0 时，无需再次登录，也不读取 CLI 的登录文件
syc proxy --port 7897

# 局域网入口，默认无需密码
syc proxy --port 7897 --allow-lan

# 可选认证：指定用户名后启用，密码从环境变量读取
$env:SYC_PROXY_PASSWORD = '替换为你自己的代理密码'
syc proxy --port 7897 --allow-lan --username mesh
```

Linux 对应：`SYC_PROXY_PASSWORD='自己的密码' syc proxy --allow-lan --username mesh`。
`--iface` 默认 `syc0`。CLI 每 5 秒打印状态，Ctrl+C 停止并释放已有连接。端口被占用时直接报错。
这里的代理密码独立于 FileSync 登录密码，不应复用账号密码。

Windows 的 `syc up/down` 使用软件自带的 `wireguard.exe`，隧道操作需要管理员权限；代理自身不申请提权。
打包 CLI 时，将仓库内固定版本 `wireguard.exe` 放在 `syc.exe` 同目录或 `wireguard/` 子目录。
开发构建也会查找仓库内的桌面资源，不使用用户另装的 WireGuard。Windows 仅操作本软件的 `syc0`。
CLI 会话仍沿用原有存储格式；Unix 的 0600/0700 不适用于 Windows，不宣称 Windows 会话文件具有同样权限保证。

## 其他软件接入

- 本机 HTTP 代理地址：`http://127.0.0.1:7897`。
- 本机 SOCKS5 地址：`socks5h://127.0.0.1:7897`（域名在代理端解析）。
- 局域网：把 `127.0.0.1` 替换为运行代理电脑的局域网 IP，仅在开启「需要密码」时配置代理认证。
- 示例：`curl --proxy http://127.0.0.1:7897 http://172.1.0.254/file/v1/ping`（当前 nginx 入口）。
- 设置 HTTP/SOCKS5 的软件可以访问其协议支持的 TCP 服务；无法配置代理的软件仍需系统隧道或自行配置代理适配。

不修改系统代理、DNS、默认路由或防火墙。LAN 入站是否可达取决于现有防火墙设置。
HTTP Basic 与 SOCKS5 用户名/密码认证本身不加密，仅用于可信局域网；目标 TLS 流量通过 CONNECT 原样转发。
不支持 SOCKS UDP ASSOCIATE、BIND、ICMP、IPv6 组网地址或公网代理。
代理最多同时接入 128 个连接，握手/解析/连接阶段总超时 10 秒，HTTP 头上限 16 KiB，按流转发，无整文件缓存。

## 验证

`cargo test --manifest-path filesync-cli/mesh-proxy/Cargo.toml` 运行共享库测试。
正向 TCP 测试使用测试构建专用的 loopback 目标例外；正式构建只允许组网地址。
测试覆盖 HTTP 转发、CONNECT、SOCKS5 认证与半关闭、目标拒绝、认证失败、关闭连接和监听释放。
这些本机测试不代替 Windows 隧道、跨设备 LAN 或 IPv4/IPv6 入口的实际联调。

2026-10-19 已在 Windows 使用正式 CLI 构建和现有 `syc0 / 172.1.0.1` 实测：
临时回环代理 `127.0.0.1:17997` 分别经 HTTP、SOCKS5 和 HTTPS CONNECT 访问
`172.1.0.254/file/v1/ping`，三者均返回 `HTTP 200`、`code:200`、`message:pong`。
HTTPS 因使用 IP 访问域名证书，仅该次 curl 检查使用 `--insecure`，代理不解密 TLS。
直连后端 8991 未响应，使用 nginx 80/443 入口成功；没有据此修改后端监听或防火墙。
测试结束 Ctrl+C 停止代理。跨设备 LAN、Windows CLI 安装/卸载隧道和桌面点击操作尚未实测。
