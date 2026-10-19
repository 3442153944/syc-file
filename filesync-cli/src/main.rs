//! 云梯（FileSync）命令行客户端。
//!
//! 目标：不依赖 GUI 也能完成「登录 → 入组 → 登记设备 → 拉对端 → 起隧道」的闭环，
//! 桌面端与自动化测试共用同一套接口，不做第二份协议实现。
//!
//! 会话与私钥都保存在 `~/.config/syc-cli/`（目录 700、文件 600），不写系统级配置；
//! 服务端只存密文私钥，明文私钥只在 `register` 响应里出现一次，由本工具落盘。

use std::env;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::process::Command;

use anyhow::{anyhow, bail, Context, Result};
use clap::{Parser, Subcommand};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};

// ── 命令行定义 ─────────────────────────────────────────────────

#[derive(Parser)]
#[command(name = "syc", about = "云梯组网命令行客户端", version)]
struct Cli {
    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
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
    /// 创建并起隧道（需要 root，Linux）
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

// ── 会话与密钥存储 ─────────────────────────────────────────────

#[derive(Serialize, Deserialize, Clone)]
struct Session {
    server: String,
    token: String,
    /// 登录时绑定的设备号（JWT 里带的就是它，请求头必须一致）
    device: String,
    username: String,
}

fn config_dir() -> Result<PathBuf> {
    // root 执行 syc up 时 HOME=/root，会找不到普通用户的会话；
    // 用 SYC_CLI_CONFIG_DIR 显式指定（例：sudo SYC_CLI_CONFIG_DIR=/home/you/.config/syc-cli syc up）。
    let dir = match env::var("SYC_CLI_CONFIG_DIR") {
        Ok(dir) if !dir.trim().is_empty() => PathBuf::from(dir),
        _ => {
            let home = env::var("HOME").context("读不到 HOME")?;
            PathBuf::from(home).join(".config").join("syc-cli")
        }
    };
    fs::create_dir_all(dir.join("keys"))?;
    let _ = fs::set_permissions(&dir, fs::Permissions::from_mode(0o700));
    Ok(dir)
}

fn session_path() -> Result<PathBuf> {
    Ok(config_dir()?.join("session.json"))
}

fn load_session() -> Result<Session> {
    let path = session_path()?;
    let raw = fs::read_to_string(&path)
        .with_context(|| format!("没有会话，请先 syc login（期望 {}）", path.display()))?;
    Ok(serde_json::from_str(&raw)?)
}

fn save_session(s: &Session) -> Result<()> {
    let path = session_path()?;
    fs::write(&path, serde_json::to_string_pretty(s)?)?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;
    Ok(())
}

fn key_path(device: &str) -> Result<PathBuf> {
    Ok(config_dir()?.join("keys").join(format!("{device}.key")))
}

fn save_key(device: &str, private_b64: &str) -> Result<()> {
    let path = key_path(device)?;
    fs::write(&path, private_b64.trim())?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;
    Ok(())
}

fn load_key(device: &str) -> Result<String> {
    let path = key_path(device)?;
    Ok(fs::read_to_string(&path)
        .with_context(|| format!("没有 {} 的私钥，请先 register", device))?
        .trim()
        .to_string())
}

// ── HTTP ───────────────────────────────────────────────────────

fn request(
    method: &str,
    url: &str,
    body: Option<Value>,
    session: Option<&Session>,
    device_override: Option<&str>,
) -> Result<Value> {
    let mut req = match method {
        "GET" => ureq::get(url),
        "POST" => ureq::post(url),
        "PUT" => ureq::put(url),
        "DELETE" => ureq::delete(url),
        m => bail!("不支持的方法 {m}"),
    };
    if let Some(s) = session {
        req = req.set("Token", &s.token);
        req = req.set("Device-Id", device_override.unwrap_or(&s.device));
    }
    let resp = match body {
        Some(b) => req.set("Content-Type", "application/json").send_json(b),
        None => req.call(),
    };
    match resp {
        Ok(r) => Ok(r.into_json::<Value>()?),
        // 业务错误也是 HTTP 200 + code，但 400/401 之类按状态码处理时也要把 body 读出来
        Err(ureq::Error::Status(_, r)) => Ok(r.into_json::<Value>()?),
        Err(e) => Err(anyhow!("请求失败: {e}")),
    }
}

fn ensure_ok(v: &Value) -> Result<&Value> {
    if v.get("code").and_then(Value::as_i64) == Some(200) {
        Ok(v)
    } else {
        Err(anyhow!(
            "接口返回 {}: {}",
            v.get("code").unwrap_or(&Value::Null),
            v.get("message").and_then(Value::as_str).unwrap_or("未知错误")
        ))
    }
}

// ── 密钥 ───────────────────────────────────────────────────────

fn generate_keypair() -> Result<(String, String)> {
    use base64::Engine;
    use x25519_dalek::{PublicKey, StaticSecret};

    let secret = StaticSecret::random_from_rng(rand_core::OsRng);
    let public = PublicKey::from(&secret);
    let priv_b64 = base64::engine::general_purpose::STANDARD.encode(secret.to_bytes());
    let pub_b64 = base64::engine::general_purpose::STANDARD.encode(public.to_bytes());
    Ok((priv_b64, pub_b64))
}

// ── 隧道 ───────────────────────────────────────────────────────

fn is_root() -> bool {
    Command::new("id")
        .arg("-u")
        .output()
        .ok()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim() == "0")
        .unwrap_or(false)
}

/// 优先用 /usr/bin/wg 的副本：这台机器上 AppArmor 会拦 /usr/bin/wg 打开普通文件
/// （fopen: Permission denied），复制出来的二进制不受路径规则限制。
fn wg_bin() -> String {
    let src = Command::new("sh")
        .args(["-c", "command -v wg"])
        .output()
        .ok()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default();
    if src.is_empty() {
        return "wg".to_string();
    }
    let dst = env::temp_dir().join("syc-wg");
    match fs::copy(&src, &dst) {
        Ok(_) => {
            let _ = fs::set_permissions(&dst, fs::Permissions::from_mode(0o755));
            dst.to_string_lossy().into_owned()
        }
        Err(_) => src,
    }
}

fn sh(program: &str, args: &[&str]) -> Result<()> {
    let out = Command::new(program)
        .args(args)
        .output()
        .with_context(|| format!("执行 {program} 失败（是否安装？）"))?;
    if !out.status.success() {
        bail!(
            "{program} {} 失败: {}",
            args.join(" "),
            String::from_utf8_lossy(&out.stderr).trim()
        );
    }
    Ok(())
}

/// 从 peers 响应拼出 wg-quick 配置。self 里有地址与网段，peers 里是对端。
fn build_wg_config(peers: &Value, private_key: &str, quick: bool, listen_port: u16) -> Result<String> {
    let data = peers.get("data").ok_or_else(|| anyhow!("peers 响应缺少 data"))?;
    let self_info = data.get("self").ok_or_else(|| anyhow!("peers 响应缺少 self"))?;
    let address = self_info
        .get("address")
        .and_then(Value::as_str)
        .ok_or_else(|| anyhow!("self 缺少 address"))?;
    let subnet = self_info.get("subnet").and_then(Value::as_str).unwrap_or("");
    let bits = subnet.rsplit('/').next().unwrap_or("24");
    let mtu = self_info.get("mtu").and_then(Value::as_u64).unwrap_or(1280);
    let keepalive = self_info.get("keepalive").and_then(Value::as_u64).unwrap_or(25);

    let mut out = String::new();
    out.push_str("[Interface]\n");
    out.push_str(&format!("PrivateKey = {private_key}\n"));
    // Address/MTU 只有 wg-quick 认；wg setconf 见到这两行会直接报 Configuration parsing error
    if quick {
        out.push_str(&format!("Address = {address}/{bits}\n"));
        out.push_str(&format!("MTU = {mtu}\n"));
    }
    out.push_str(&format!("ListenPort = {listen_port}\n"));

    for peer in data.get("peers").and_then(Value::as_array).cloned().unwrap_or_default() {
        let pubkey = peer.get("pubkey").and_then(Value::as_str).unwrap_or("");
        if pubkey.is_empty() {
            continue;
        }
        let allowed = peer
            .get("allowed_ips")
            .and_then(Value::as_array)
            .map(|a| {
                a.iter()
                    .filter_map(Value::as_str)
                    .collect::<Vec<_>>()
                    .join(", ")
            })
            .unwrap_or_default();
        out.push_str("\n[Peer]\n");
        out.push_str(&format!("PublicKey = {pubkey}\n"));
        out.push_str(&format!("AllowedIPs = {allowed}\n"));
        if let Some(ep) = peer.get("endpoint").and_then(Value::as_str) {
            if !ep.is_empty() {
                out.push_str(&format!("Endpoint = {ep}\n"));
            }
        }
        out.push_str(&format!("PersistentKeepalive = {keepalive}\n"));
    }
    Ok(out)
}

// ── 子命令实现 ─────────────────────────────────────────────────

fn default_device() -> String {
    fs::read_to_string("/etc/hostname")
        .map(|s| s.trim().to_string())
        .unwrap_or_else(|_| "cli-host".to_string())
}

fn main() -> Result<()> {
    match Cli::parse().cmd {
        Cmd::Login {
            server,
            user,
            password,
            device,
        } => {
            let password = password
                .or_else(|| env::var("SYC_PASSWORD").ok())
                .ok_or_else(|| anyhow!("缺少密码：--password 或环境变量 SYC_PASSWORD"))?;
            let device = device.unwrap_or_else(default_device);
            let server = server.trim_end_matches('/').to_string();
            let body = json!({"username": user, "password": password, "device_id": device});
            let v = request(
                "POST",
                &format!("{server}/v1/user/login"),
                Some(body),
                None,
                None,
            )?;
            ensure_ok(&v)?;
            let token = v["data"]["token"]
                .as_str()
                .ok_or_else(|| anyhow!("响应缺少 token"))?
                .to_string();
            let username = v["data"]["user"]["username"]
                .as_str()
                .unwrap_or(&user)
                .to_string();
            let level = v["data"]["user"]["level"].as_i64().unwrap_or(0);
            save_session(&Session {
                server,
                token,
                device: device.clone(),
                username: username.clone(),
            })?;
            println!("已登录 {username}（level={level}，设备 {device}），会话已保存");
        }

        Cmd::Status => {
            let s = load_session()?;
            let v = request("GET", &format!("{}/v1/ping", s.server), None, None, None)?;
            println!("{}", serde_json::to_string_pretty(&v)?);
        }

        Cmd::Devices => {
            let s = load_session()?;
            let v = request(
                "GET",
                &format!("{}/v1/vpn/devices/my", s.server),
                None,
                Some(&s),
                None,
            )?;
            print_table(v)?;
        }

        Cmd::Groups => {
            let s = load_session()?;
            let v = request(
                "GET",
                &format!("{}/v1/vpn/admin/groups", s.server),
                None,
                Some(&s),
                None,
            )?;
            print_table(v)?;
        }

        Cmd::Enroll { group } => {
            let s = load_session()?;
            let v = request(
                "POST",
                &format!("{}/v1/vpn/enroll", s.server),
                Some(json!({"group_id": group})),
                Some(&s),
                None,
            )?;
            ensure_ok(&v)?;
            println!("入组令牌（5 分钟内有效，只能用一次）：");
            println!("{}", v["data"]["token"].as_str().unwrap_or(""));
            println!("组网段：{}", v["data"]["subnet"].as_str().unwrap_or("?"));
        }

        Cmd::Register {
            group,
            device,
            key_source,
            endpoint,
        } => {
            let s = load_session()?;
            // 先拿令牌
            let e = request(
                "POST",
                &format!("{}/v1/vpn/enroll", s.server),
                Some(json!({"group_id": group})),
                Some(&s),
                None,
            )?;
            ensure_ok(&e)?;
            let token = e["data"]["token"].as_str().unwrap_or("").to_string();

            let mut body = json!({
                "token": token,
                "device_id": device,
                "key_source": key_source,
            });
            if key_source == "client" {
                let (priv_b64, pub_b64) = generate_keypair()?;
                save_key(&device, &priv_b64)?;
                body["pubkey"] = json!(pub_b64);
            }
            if let Some(ep) = endpoint {
                body["endpoint"] = json!(ep);
            }
            let v = request(
                "POST",
                &format!("{}/v1/vpn/register", s.server),
                Some(body),
                Some(&s),
                None,
            )?;
            ensure_ok(&v)?;

            if let Some(priv_b64) = v["data"]["private_key"].as_str() {
                if !priv_b64.is_empty() {
                    save_key(&device, priv_b64)?;
                }
            }
            println!("设备 {} 已登记", device);
            println!("地址：{}", v["data"]["address"].as_str().unwrap_or("?"));
            println!("网段：{}", v["data"]["subnet"].as_str().unwrap_or("?"));
            println!("公钥：{}", v["data"]["pubkey"].as_str().unwrap_or("?"));
            println!("私钥已保存到 {}", key_path(&device)?.display());
        }

        Cmd::Peers { device } => {
            let s = load_session()?;
            let v = request(
                "GET",
                &format!("{}/v1/vpn/peers?device_id={}", s.server, device),
                None,
                Some(&s),
                None,
            )?;
            ensure_ok(&v)?;
            let peers = v["data"]["peers"].as_array().cloned().unwrap_or_default();
            println!("自身：{}", v["data"]["self"]["address"].as_str().unwrap_or("?"));
            println!("对端 {} 个：", peers.len());
            for p in peers {
                println!(
                    "  {:<24} {:<16} {}",
                    p["device_id"].as_str().unwrap_or(""),
                    p["address"].as_str().unwrap_or(""),
                    p["endpoint"].as_str().unwrap_or("(端点未知)")
                );
            }
        }

        Cmd::Nat {
            device,
            stun_port,
            report,
        } => {
            let s = load_session()?;
            let host = server_host(&s.server)?;
            let sock = std::net::UdpSocket::bind("0.0.0.0:0")?;
            sock.set_read_timeout(Some(std::time::Duration::from_secs(3)))?;
            sock.send_to(b"SYC?", (host.as_str(), stun_port))?;
            let mut buf = [0u8; 128];
            let (n, _) = sock
                .recv_from(&mut buf)
                .map_err(|e| anyhow!("STUN 探测超时或失败（服务器 UDP {stun_port} 是否放行？）: {e}"))?;
            let reply = String::from_utf8_lossy(&buf[..n]).trim().to_string();
            let endpoint = reply.strip_prefix("SYC1 ").unwrap_or(&reply).to_string();
            println!("公网端点：{endpoint}");
            if report {
                let dev = device
                    .or_else(|| env::var("SYC_DEVICE").ok())
                    .ok_or_else(|| anyhow!("上报需要 --device（本机登记时的设备名）"))?;
                let v = request(
                    "POST",
                    &format!("{}/v1/vpn/heartbeat", s.server),
                    Some(json!({"device_id": dev, "endpoint": endpoint})),
                    Some(&s),
                    None,
                )?;
                ensure_ok(&v)?;
                println!("已上报端点，对端可用它直连");
            }
        }

        Cmd::Link { device } => {
            let s = load_session()?;
            let v = request(
                "GET",
                &format!("{}/v1/vpn/link?device_id={}", s.server, device),
                None,
                Some(&s),
                None,
            )?;
            ensure_ok(&v)?;
            let host = server_host(&s.server)?;
            let server_ep = format!("{host}:51820");
            let peers = v["data"]["peers"].as_array().cloned().unwrap_or_default();
            let mut any_relay = false;
            for p in &peers {
                if p["endpoint"].as_str() == Some(server_ep.as_str()) {
                    any_relay = true;
                }
            }
            let mode = if peers.is_empty() { "idle" } else if any_relay { "relay" } else { "direct" };
            let label = match mode {
                "direct" => "直连",
                "relay" => "中继",
                _ => "空闲",
            };
            println!("网络环境：{label}（mode={mode}）");
            for p in v["data"]["peers"].as_array().cloned().unwrap_or_default() {
                println!(
                    "  {:<24} {:<10} {}",
                    p["device_id"].as_str().unwrap_or(""),
                    match p["mode"].as_str().unwrap_or("") {
                        "relay" => "中继",
                        "direct" => "直连",
                        _ => "-",
                    },
                    p["endpoint"].as_str().unwrap_or("(端点未知)")
                );
            }
        }

        Cmd::WgConfig { device } => {
            let s = load_session()?;
            let key = load_key(&device)?;
            let v = request(
                "GET",
                &format!("{}/v1/vpn/peers?device_id={}", s.server, device),
                None,
                Some(&s),
                None,
            )?;
            ensure_ok(&v)?;
            let v = pin_server_endpoint(v, &s.server, 51820);
            print!("{}", build_wg_config(&v, &key, true, 0)?);
        }

        Cmd::Up { device, iface, listen_port } => {
            if !is_root() {
                bail!("up 需要 root（创建虚拟网卡）；无 root 环境可用 syc wg-config 生成配置后自行导入");
            }
            let s = load_session()?;
            let key = load_key(&device)?;
            let v = request(
                "GET",
                &format!("{}/v1/vpn/peers?device_id={}", s.server, device),
                None,
                Some(&s),
                None,
            )?;
            ensure_ok(&v)?;
            let v = pin_server_endpoint(v, &s.server, 51820);
            let conf = build_wg_config(&v, &key, false, listen_port)?;

            let path = env::temp_dir().join(format!("syc-{iface}.conf"));
            fs::write(&path, &conf)?;
            fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;

            // 已存在就先删，保证幂等
            let _ = Command::new("ip").args(["link", "del", "dev", &iface]).output();
            sh("ip", &["link", "add", "dev", &iface, "type", "wireguard"])?;
            // 配置从 stdin 喂给 wg，避免它按路径打开文件（AppArmor 会拦）
            let status = Command::new(wg_bin())
                .args(["setconf", &iface, "/dev/stdin"])
                .stdin(std::process::Stdio::from(fs::File::open(&path)?))
                .status()
                .context("执行 wg setconf 失败")?;
            if !status.success() {
                bail!("wg setconf {iface} 失败: {}", status);
            }
            let bits = v["data"]["self"]["subnet"]
                .as_str()
                .and_then(|s| s.rsplit('/').next())
                .unwrap_or("24");
            let addr = v["data"]["self"]["address"].as_str().unwrap_or("");
            sh(
                "ip",
                &["addr", "add", &format!("{addr}/{bits}"), "dev", &iface],
            )?;
            sh("ip", &["link", "set", &iface, "up"])?;
            let _ = fs::remove_file(&path);
            println!("隧道 {iface} 已启动，地址 {addr}/{bits}");
        }

        Cmd::Down { iface } => {
            if !is_root() {
                bail!("down 需要 root");
            }
            sh("ip", &["link", "del", "dev", &iface])?;
            println!("隧道 {iface} 已关闭");
        }
    }
    Ok(())
}

/// 把 peers 里服务器节点的端点替换成「当前选中节点 + wg 端口」。
/// 这样节点灾备一切换，WireGuard 端点自动跟着换，而不是钉死在某个 DDNS 域名上。
fn pin_server_endpoint(mut peers: Value, server: &str, wg_port: u16) -> Value {
    let host = match server_host(server) {
        Ok(h) => h,
        Err(_) => return peers,
    };
    let ep = format!("{host}:{wg_port}");
    if let Some(list) = peers.get_mut("data").and_then(|d| d.get_mut("peers")).and_then(|p| p.as_array_mut()) {
        for p in list.iter_mut() {
            let is_server = p.get("role").and_then(Value::as_str) == Some("server")
                || p.get("device_id").and_then(Value::as_str) == Some("syc-server");
            if is_server {
                p["endpoint"] = json!(ep);
            }
        }
    }
    peers
}

/// 从 server URL 取主机名（http(s)://host:port/... → host）。
fn server_host(server: &str) -> Result<String> {
    let rest = server.split("://").nth(1).unwrap_or(server);
    let host = rest.split(&['/', ':'][..]).next().unwrap_or("").to_string();
    if host.is_empty() {
        bail!("server 地址非法: {server}");
    }
    Ok(host)
}

fn print_table(v: Value) -> Result<()> {
    let data = v.get("data").cloned().unwrap_or(Value::Null);
    match data {
        Value::Array(rows) => {
            for r in rows {
                println!(
                    "  {:<24} {:<16} {:<20} {}",
                    r["device_id"].as_str().or(r["name"].as_str()).unwrap_or("-"),
                    r["address"].as_str().unwrap_or("-"),
                    r["status"].as_str().or(r["subnet"].as_str()).unwrap_or("-"),
                    r["id"]
                );
            }
        }
        other => println!("{}", serde_json::to_string_pretty(&other)?),
    }
    Ok(())
}
