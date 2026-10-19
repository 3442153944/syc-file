# filesync-cli（`syc`）

云梯组网的命令行客户端：不依赖 GUI，完成「登录 → 入组 → 登记设备 → 拉对端 → 起隧道」。

桌面端、自动化测试、无头服务器共用它，协议只有一份实现（`/v1/vpn/*`）。

## 构建

```bash
cd filesync-cli
cargo build --release
# 产物：target/release/syc
```

依赖只有 clap / ureq / serde / x25519-dalek，不引 Tauri 或 WebKit，Linux 无图形环境也能编译运行。

## 命令

| 命令 | 说明 |
|---|---|
| `syc login --server URL --user U [--password P] [--device D]` | 登录并保存会话（密码也可用环境变量 `SYC_PASSWORD`） |
| `syc status` | 探测 `/v1/ping` |
| `syc devices` | 当前账号已登记的设备 |
| `syc groups` | 可见的组（含网段） |
| `syc enroll --group ID` | 只签发一次性入组令牌 |
| `syc register --group ID --device NAME [--key-source server\|client]` | 登记设备；`server` 时私钥由服务端生成并托管，本地落盘 600 |
| `syc peers --device NAME` | 按 ACL 裁剪的对端 |
| `syc wg-config --device NAME` | 输出 wg-quick 配置（可直接导入系统 WireGuard） |
| `syc up --device NAME [--iface syc0]` | 创建并拉起隧道（**需要 root**） |
| `syc down [--iface syc0]` | 关闭隧道（需要 root） |

## 典型流程

```bash
export SYC_PASSWORD='***'
syc login --server https://example.com --user admin --device my-laptop
syc groups                       # 找一个 group id
syc register --group 3 --device my-laptop
syc peers --device my-laptop
sudo syc up --device my-laptop   # 非 root 环境改用 wg-config 自行导入
```

## 存储位置

```
~/.config/syc-cli/session.json     会话（token、server、登录设备号）  600
~/.config/syc-cli/keys/<device>.key 设备私钥                            600
```

目录 700。服务端只保存密文私钥（AES-256-GCM，AAD 绑定设备号），明文私钥只在 `register` 响应里出现一次。

## 注意

- `Device-Id` 请求头必须与登录时的设备号一致，否则认证中间件按未登录处理；
- `up` 依赖 `ip` 与 `wg`（wireguard-tools），且需要 root；无 root 时先 `wg-config` 再交给系统侧导入；
- 密钥轮换：对同一 `--device` 重新 `register` 会自动换公钥，旧公钥进 `vpn_device_history`。
