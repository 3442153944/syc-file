# deploy/ — 外部服务配置归集

后端启动时会拉起几个外部进程（内网穿透 frpc × 2、动态域名 ddns-go），它们各自的配置
文件统一收在这里，由 `config/config.yaml` 的 `supervisor` 段引用。

**本目录除模板和本文件外一律不进版本库**（`frpc.toml` 里有 frps 的 `auth.token`，
`ddns_go_config.yaml` 里有 Cloudflare API Key）。忽略规则在仓库根 `.gitignore`，
写法是「整目录忽略 + 显式放行 `*.example.*`」，新增敏感文件不会漏。

## 目录

```
deploy/
  frp/
    frpc.toml               主入口隧道（ddns.sunyuanling.cn）      ← 不入库
    frpc_akile.toml         东京备用隧道（jp.sunyuanling.cn:8443） ← 不入库
    frpc.example.toml       模板
  ddns/
    ddns_go_config.yaml     ddns-go 配置                          ← 不入库
    ddns_go_config.example.yaml  模板
```

可执行文件（`frpc.exe` / `ddns-go.exe`）留在各自的安装目录，不进仓库——它们是十几 MB
的二进制，路径写在 `supervisor.processes[].command` 里。

## 换机器部署

1. 复制 `config/config.example.yaml` 为 `config/config.yaml`，填数据库密码和 `auth.secret`
2. 复制本目录下每个 `*.example.*` 为去掉 `.example` 的文件名，填 token / API Key
3. 改 `config.yaml` 里 `supervisor.processes[].command` 为本机的 exe 路径
4. 启动后端，日志里会打印每个子进程的 pid

## 行为说明

- **已在运行就不抢**：`skip_if_running: true` 时，后端启动前会按「可执行文件路径 +
  命令行里的配置文件名」查一遍现有进程。frpc 用相同 proxy 名重复连同一个 frps 会被
  拒绝（`proxy [xxx] already exists`），结果是新旧客户端都用不上，所以这项对 frpc 必须开。
  也因此，如果你还在用 `E:\内网穿透\frp\start.bat` 手动起 frpc，后端会礼让不管——
  想改由后端托管，先把手动起的那批停掉。
- **死了会重启**：`restart_delay_seconds` 秒后重新拉起，直到后端退出。
- **日志落盘**：子进程的 stdout/stderr 写到 `log/supervisor/<name>.log`。
- **跟着后端一起退出**：后端收到 Ctrl+C / SIGTERM 会先停 HTTP 再杀子进程。
  这一步不能省——留下孤儿 frpc，下次启动就会撞上上面说的 proxy 冲突。

## 注意

`ddns-go` 只更新 `ddns.sunyuanling.cn` 的 **AAAA** 记录（家宽 IPv6 前缀会变），
配置里 `ipv4.enable = false` 是故意的：A 记录固定指向 VPS 走 frp 隧道，不需要动态更新。
