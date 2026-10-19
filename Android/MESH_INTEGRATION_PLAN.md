# Android 组网接入准备（2026-10-19）

本轮只准备方案，不修改 Android 运行代码、依赖或权限。

## 当前代码与可复用机制

- `network/request.kt` 已提供 OkHttp、Token、Device-Id 和节点地址管理；组网协调 API 应复用该入口，不另建账号体系。
- `network/Authmanager.kt` 有认证失效事件，可作为关闭组网会话和隧道的入口。
- `service/DownloadService.kt` 已有前台服务与通知模式，但负责下载任务，不适合直接承担 VPN 生命周期。
- 当前 Manifest 未声明 `VpnService`，Gradle 未引入 WireGuard Android tunnel 库，也没有组网专属 API/UI。
- 服务端已有 `/v1/vpn/auto-enroll`、`identity`、`peers`、`heartbeat`、`link`、`devices/my`；普通用户接入优先用这些接口，避免依赖管理员 overview。

## 两条使用路径

1. **使用桌面代理**：手机和电脑处于可达的局域网，在支持代理配置的应用内填入电脑地址、混合端口和认证。无需在 Android 建 VPN；覆盖范围取决于该应用的 HTTP/SOCKS5 支持，不承诺系统所有 App 自动进入组网。
2. **Android 自身组网**：采用 WireGuard 官方 Android tunnel 库的用户态后端。Android 使用 `VpnService`，首次连接由系统向用户申请 VPN 授权。只路由服务端下发的组网目标，不接管公网默认路由或系统 DNS。

Android 同一用户/工作资料仅允许一个活动 VPN；运行 FileSync VPN 会影响同一资料内正在使用的其他 VPN。需要在连接前明确展示系统行为，不能默认假设能与 Clash VPN 同时运行。

## 建议实现拆分

| 模块 | 职责 |
|---|---|
| `mesh/MeshApi.kt` | 协调接口与 DTO，复用 Request 的认证/节点选择 |
| `mesh/MeshController.kt` | 状态机、用户启停、设备登记、取消任务、StateFlow |
| `mesh/MeshTunnelBackend.kt` | WireGuard GoBackend/Tunnel 适配、组网路由、端点更新 |
| `mesh/MeshHeartbeat.kt` | 跟随连接生命周期的心跳与 peers 更新，单协程防重入 |
| `ui/screen/mesh/MeshScreen.kt` | 地址、真实隧道/握手状态、传输量、设备和错误、连接按钮 |

每个文件尽量小于 1000 行。优先利用官方后端自身的 VPN service，不重复声明第二个与其争用 TUN 的服务；如需额外前台协调服务，先核对目标 SDK 的前台服务类型要求。

## 接入顺序与验收

1. **API 与身份**：登记设备 → 用当前 JWT 获取 identity/peers。身份不写普通 Preferences 或日志，页面销毁、退出登录、401 或撤销时清理运行状态。持久缓存非敏感设备信息时按账号与节点隔离。
2. **隧道闭环**：连接按钮触发系统 VPN 授权；拒绝授权不显示“已连接”。官方后端加载地址、MTU、peers 和明确的 AllowedIPs。不能把协调 API 可达当作隧道可达。
3. **会话与网络切换**：心跳跟随活动会话；Wi-Fi/移动网络切换时刷新端点和 peers；关闭时取消全部任务，避免重复心跳/重连循环。界面分别表示启动中、隧道运行、握手/业务访问结果。
4. **权限生命周期**：用户断开、系统撤销 VPN、退出登录、token 失效、设备封禁/停用后实际关闭隧道；系统杀进程与重建后不得使用过期身份自动恢复。
5. **真机验收**：本机普通 adb 调试，不使用 su。验证手机→服务器、手机↔Windows 的 TCP 业务访问、直连与中继实绩、熄屏、前后台、网络切换、取消授权、撤销设备、断开后路由恢复，以及大文件传输时的内存和电量表现。

## 已知前置问题

- 服务端 IPv4-only 入口到服务器节点的限制仍需独立验证；Android 页面不能用“已连上 VPN”掩盖服务器不可达。
- 桌面代理当前只支持 TCP，不能据此宣称 Android UDP、游戏或 ICMP 已支持。
- 引入官方库前需核对固定版本、ABI、最小 SDK、许可证与 Gradle 兼容性；本轮未增加依赖，也未执行 Android 编译/安装。
- 现有服务端设备撤销是否立即终止已经建立的直连数据面，必须用真实连接验证，不能仅检查协调接口状态。

## 官方依据

- Android VPN 服务、授权和单 VPN 限制：https://developer.android.com/develop/connectivity/vpn
- WireGuard 官方嵌入指南（Android tunnel 库）：https://www.wireguard.com/embedding/
