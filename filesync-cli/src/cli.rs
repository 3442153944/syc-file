use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(name = "syc", about = "云梯组网命令行客户端", version)]
pub struct Cli {
    #[command(subcommand)]
    pub cmd: Cmd,
}

#[derive(Subcommand)]
pub enum Cmd {
    /// 暴露 HTTP/SOCKS5 混合端口供本机或局域网软件访问组网，Ctrl+C 停止
    Proxy {
        #[arg(long, default_value_t = 7897)]
        port: u16,
        #[arg(long)]
        allow_lan: bool,
        #[arg(long)]
        username: Option<String>,
        #[arg(long, default_value = "syc0")]
        iface: String,
    },
    /// 登录并保存会话
    Login {
        /// 服务端地址，例如 http://127.0.0.1:8992
        #[arg(long)]
        server: String,
        #[arg(long)]
        user: String,
        /// 不传则读环境变量 SYC_PASSWORD
        #[arg(long)]
        password: Option<String>,
        /// 登录设备号，默认取主机名
        #[arg(long)]
        device: Option<String>,
    },
    /// 探测服务端 /v1/ping
    Status,
    /// 当前账号已登记的设备
    Devices,
    /// 当前账号可见的组
    Groups,
    /// 签发一次性入组令牌
    Enroll {
        #[arg(long)]
        group: u64,
    },
    /// 用一次性令牌登记设备；key_source=server 时私钥由服务端生成并托管
    Register {
        #[arg(long)]
        group: u64,
        #[arg(long)]
        device: String,
        #[arg(long, default_value = "server")]
        key_source: String,
        #[arg(long)]
        endpoint: Option<String>,
    },
    /// 拉取按 ACL 裁剪后的对端
    Peers {
        #[arg(long)]
        device: String,
    },
    /// 探测公网端点（STUN-lite），可选择顺手通过 heartbeat 上报
    Nat {
        #[arg(long)]
        device: Option<String>,
        #[arg(long, default_value_t = 3478)]
        stun_port: u16,
        /// 探测后立即上报端点，方便对端直连
        #[arg(long)]
        report: bool,
    },
    /// 一键体检：STUN 探测 + 链路状态 + 对端列表
    Netcheck {
        #[arg(long)]
        device: String,
        #[arg(long, default_value_t = 3478)]
        stun_port: u16,
    },
    /// 对覆盖网地址做 ping（可指定网卡，强制走隧道）
    Ping {
        /// 目标覆盖地址，如 172.1.0.254
        to: String,
        #[arg(long, default_value_t = 3)]
        count: u8,
        /// 指定网卡（例：syc1），强制从隧道出去
        #[arg(long)]
        iface: Option<String>,
    },
    /// 查看各对端是直连还是经中继
    Link {
        #[arg(long)]
        device: String,
    },
    /// 打印 WireGuard 配置（wg-quick 格式）
    WgConfig {
        #[arg(long)]
        device: String,
    },
    /// 创建并起隧道（Linux root / Windows 管理员）
    Up {
        #[arg(long)]
        device: String,
        #[arg(long, default_value = "syc0")]
        iface: String,
        /// 本地监听端口；0 = 内核自动分配（同机多客户端时避免撞端口）
        #[arg(long, default_value_t = 0)]
        listen_port: u16,
    },
    /// 关闭隧道
    Down {
        #[arg(long, default_value = "syc0")]
        iface: String,
    },
}
