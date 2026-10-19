//! 桌面端组网模块（薄封装）。
//!
//! 真正的协议实现只有一份：`filesync-cli`（`syc`）。桌面端不复制一套 HTTP/WireGuard
//! 逻辑，而是把 `syc` 当 sidecar 调用，避免两端行为漂移。
//!
//! 约定：
//!   - `syc` 需在 PATH（开发机）或随安装包放在资源目录并加入 PATH；
//!   - 设备名由前端保存到 localStorage.syc_vpn_device，注册后传给这些命令；
//!   - `vpn_up/down` 需要管理员权限（Windows 服务模式 / Linux polkit）。
//!
//! 前端 NodeSwitcher 的「直连/中继」标签读 /v1/vpn/link，数据源就是 vpn_link 的返回。

use std::io::{BufRead, BufReader};
use std::process::{Command, Stdio};
use tauri::{Emitter, Manager};

fn run_syc(args: &[&str]) -> Result<String, String> {
    let out = Command::new("syc")
        .args(args)
        .output()
        .map_err(|e| format!("未找到 syc 命令行客户端（需随桌面端安装）: {e}"))?;
    if !out.status.success() {
        let err = String::from_utf8_lossy(&out.stderr).trim().to_string();
        let stdout = String::from_utf8_lossy(&out.stdout).trim().to_string();
        return Err(if err.is_empty() { stdout } else { err });
    }
    Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
}

/// 探测公网端点并通过 heartbeat 上报（打洞前必须做一次，之后客户端每 5s 保活）。
#[tauri::command]
pub fn vpn_nat(device: String) -> Result<String, String> {
    run_syc(&["nat", "--device", &device, "--report"])
}

/// 当前链路上报：直连 / 中继，以及每个对端的端点。
#[tauri::command]
pub fn vpn_link(device: String) -> Result<String, String> {
    run_syc(&["link", "--device", &device])
}

/// 打印该设备的 wg-quick 配置（PersistentKeepalive 固定 5s，由服务端下发）。
#[tauri::command]
pub fn vpn_wg_config(device: String) -> Result<String, String> {
    run_syc(&["wg-config", "--device", &device])
}

/// 建并拉起隧道。Linux 需要 root/CAP_NET_ADMIN，Windows 需要管理员或已安装 TAP。
#[tauri::command]
pub fn vpn_up(device: String, iface: Option<String>) -> Result<String, String> {
    let iface = iface.unwrap_or_else(|| "syc0".to_string());
    run_syc(&["up", "--device", &device, "--iface", &iface])
}

/// 关闭隧道。
#[tauri::command]
pub fn vpn_down(iface: Option<String>) -> Result<String, String> {
    let iface = iface.unwrap_or_else(|| "syc0".to_string());
    run_syc(&["down", "--iface", &iface])
}

/// 原生 STUN-lite 探测：不依赖 syc 会话，桌面端直接拿到自己的公网端点。
#[tauri::command]
pub async fn vpn_stun(host: String, port: u16) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        use std::net::UdpSocket;
        use std::time::Duration;
        let sock = UdpSocket::bind("0.0.0.0:0").map_err(|e| e.to_string())?;
        sock.set_read_timeout(Some(Duration::from_secs(3))).map_err(|e| e.to_string())?;
        sock.send_to(b"SYC?", (host.as_str(), port)).map_err(|e| e.to_string())?;
        let mut buf = [0u8; 128];
        let (n, _) = sock.recv_from(&mut buf).map_err(|e| format!("STUN 超时或端口被拦: {e}"))?;
        let reply = String::from_utf8_lossy(&buf[..n]).trim().to_string();
        Ok(reply.strip_prefix("SYC1 ").unwrap_or(&reply).to_string())
    })
    .await
    .map_err(|e| format!("STUN 任务失败: {e}"))?
}

/// ping 覆盖网地址；iface 指定时强制从隧道网卡发出（仅 Unix）。
///
/// Windows 的 ping 参数和编码都不同：`-n/-w` 且输出是 GBK，
/// 直接按 UTF-8 解码会变成乱码，所以这里分平台处理。
#[tauri::command]
pub async fn vpn_ping(target: String, count: Option<u8>, iface: Option<String>) -> Result<String, String> {
    // ping 最长要几秒；同步命令会占住 Tauri 的命令线程，页面其它请求全被卡住。
    tauri::async_runtime::spawn_blocking(move || run_ping(target, count, iface))
        .await
        .map_err(|e| format!("ping 任务失败: {e}"))?
}

fn run_ping(target: String, count: Option<u8>, iface: Option<String>) -> Result<String, String> {
    let count = count.unwrap_or(3).to_string();
    let mut cmd = Command::new("ping");
    #[cfg(windows)]
    {
        let _ = iface; // Windows ping 没有 -I
        cmd.args(["-n", &count, "-w", "2000", &target]);
    }
    #[cfg(not(windows))]
    {
        cmd.args(["-c", &count, "-W", "2"]);
        if let Some(i) = iface {
            cmd.args(["-I", &i]);
        }
        cmd.arg(&target);
    }
    let out = cmd.output().map_err(|e| format!("执行 ping 失败: {e}"))?;
    let decode = |b: &[u8]| -> String {
        #[cfg(windows)]
        {
            let (text, _, _) = encoding_rs::GBK.decode(b);
            text.into_owned()
        }
        #[cfg(not(windows))]
        {
            String::from_utf8_lossy(b).into_owned()
        }
    };
    let stdout = decode(&out.stdout);
    if out.status.success() {
        Ok(stdout)
    } else {
        Err(format!("{stdout}{}", decode(&out.stderr)))
    }
}

/// 统一的 ping 命令构造（平台参数不同）。
fn build_ping(target: &str, count: u8, iface: Option<&str>) -> Command {
    let mut cmd = Command::new("ping");
    #[cfg(windows)]
    {
        let _ = iface;
        cmd.args(["-n", &count.to_string(), "-w", "2000", target]);
    }
    #[cfg(not(windows))]
    {
        cmd.args(["-c", &count.to_string(), "-W", "2"]);
        if let Some(i) = iface {
            cmd.args(["-I", i]);
        }
        cmd.arg(target);
    }
    cmd
}

fn decode_bytes(b: &[u8]) -> String {
    #[cfg(windows)]
    {
        let (text, _, _) = encoding_rs::GBK.decode(b);
        text.into_owned()
    }
    #[cfg(not(windows))]
    {
        String::from_utf8_lossy(b).into_owned()
    }
}

/// 实时输出 ping：逐行 emit `vpn-ping-line`，结束时 emit `vpn-ping-done`。
#[tauri::command]
pub async fn vpn_ping_stream(
    app: tauri::AppHandle,
    target: String,
    count: Option<u8>,
    iface: Option<String>,
) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let mut cmd = build_ping(&target, count.unwrap_or(3), iface.as_deref());
        cmd.stdout(Stdio::piped()).stderr(Stdio::piped());
        let mut child = cmd.spawn().map_err(|e| format!("执行 ping 失败: {e}"))?;
        if let Some(out) = child.stdout.take() {
            for line in BufReader::new(out).split(b'\n') {
                let bytes = line.map_err(|e| e.to_string())?;
                let _ = app.emit("vpn-ping-line", decode_bytes(&bytes));
            }
        }
        let ok = child.wait().map(|s| s.success()).unwrap_or(false);
        let _ = app.emit("vpn-ping-done", ok);
        Ok(())
    })
    .await
    .map_err(|e| format!("ping 任务失败: {e}"))?
}

/// 本机是否存在 172.1.x 的隧道地址（用于区分「未启动隧道」和「隧道通但不通」）。
#[tauri::command]
pub fn vpn_tunnel_status() -> Result<bool, String> {
    #[cfg(windows)]
    {
        let out = Command::new("netsh")
            .args(["interface", "ipv4", "show", "addresses"])
            .output()
            .map_err(|e| e.to_string())?;
        Ok(decode_bytes(&out.stdout).contains("172.1."))
    }
    #[cfg(not(windows))]
    {
        let out = Command::new("ip").args(["-br", "addr"]).output().map_err(|e| e.to_string())?;
        Ok(String::from_utf8_lossy(&out.stdout).contains("172.1."))
    }
}

/// 保存设备私钥到应用配置目录（Windows: %APPDATA%\<app>\vpn\<device>.key）。
/// auto-enroll 只在首次返回私钥，必须在这里落盘，后续启动隧道才能用。
#[tauri::command]
pub fn vpn_save_key(app: tauri::AppHandle, device: String, key: String) -> Result<(), String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?.join("vpn");
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let path = dir.join(format!("{device}.key"));
    std::fs::write(&path, key.trim()).map_err(|e| e.to_string())?;
    Ok(())
}

fn read_key(app: &tauri::AppHandle, device: &str) -> Result<String, String> {
    let path = app
        .path()
        .app_config_dir()
        .map_err(|e| e.to_string())?
        .join("vpn")
        .join(format!("{device}.key"));
    std::fs::read_to_string(&path)
        .map(|s| s.trim().to_string())
        .map_err(|e| format!("读不到设备私钥 {}: {e}", path.display()))
}

#[derive(serde::Deserialize)]
pub struct VpnPeerInput {
    pub pubkey: String,
    pub allowed_ips: Vec<String>,
    pub endpoint: Option<String>,
}

/// 用内嵌/已安装的 WireGuard for Windows 启动隧道。
/// 配置写到 ProgramData\syc\<iface>.conf，再调 wireguard.exe 安装为系统隧道服务。
#[tauri::command]
pub fn vpn_up_embedded(
    app: tauri::AppHandle,
    device: String,
    address: String,
    subnet: String,
    keepalive: Option<u32>,
    peers: Vec<VpnPeerInput>,
) -> Result<String, String> {
    #[cfg(not(windows))]
    {
        let _ = (app, device, address, subnet, keepalive, peers);
        Err("内嵌隧道仅 Windows 使用；Linux 请用 syc up".to_string())
    }
    #[cfg(windows)]
    {
        let key = read_key(&app, &device)?;
        let bits = subnet.rsplit('/').next().unwrap_or("24");
        let ka = keepalive.unwrap_or(5);
        let mut conf = String::new();
        conf.push_str("[Interface]\n");
        conf.push_str(&format!("PrivateKey = {key}\n"));
        conf.push_str(&format!("Address = {address}/{bits}\n"));
        conf.push_str("MTU = 1280\n\n");
        for p in peers {
            conf.push_str("[Peer]\n");
            conf.push_str(&format!("PublicKey = {}\n", p.pubkey));
            conf.push_str(&format!("AllowedIPs = {}\n", p.allowed_ips.join(", ")));
            if let Some(ep) = p.endpoint.filter(|e| !e.trim().is_empty()) {
                conf.push_str(&format!("Endpoint = {ep}\n"));
            }
            conf.push_str(&format!("PersistentKeepalive = {ka}\n\n"));
        }
        let base = std::env::var("ProgramData").unwrap_or_else(|_| "C:\\ProgramData".into());
        let dir = std::path::Path::new(&base).join("syc");
        std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
        let conf_path = dir.join("syc0.conf");
        std::fs::write(&conf_path, conf).map_err(|e| e.to_string())?;

        let exe = find_wireguard_exe(&app)?;
        let out = Command::new(&exe)
            .args(["/installtunnelservice", &conf_path.to_string_lossy()])
            .output()
            .map_err(|e| format!("调用 wireguard.exe 失败: {e}"))?;
        if !out.status.success() {
            return Err(format!(
                "安装隧道失败: {}{}",
                String::from_utf8_lossy(&out.stdout),
                String::from_utf8_lossy(&out.stderr)
            ));
        }
        Ok(format!("隧道已启动（{}）", conf_path.display()))
    }
}

#[cfg(windows)]
fn find_wireguard_exe(app: &tauri::AppHandle) -> Result<std::path::PathBuf, String> {
    let mut candidates = Vec::new();
    if let Ok(res) = app.path().resource_dir() {
        candidates.push(res.join("wireguard").join("wireguard.exe"));
        candidates.push(res.join("wireguard.exe"));
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            candidates.push(dir.join("wireguard").join("wireguard.exe"));
            candidates.push(dir.join("wireguard.exe"));
        }
    }
    candidates.push(std::path::PathBuf::from(r"C:\Program Files\WireGuard\wireguard.exe"));
    candidates
        .into_iter()
        .find(|p| p.exists())
        .ok_or_else(|| "未找到 wireguard.exe：请把它放到 resources/wireguard/ 或安装 WireGuard for Windows".to_string())
}

/// 关闭内嵌隧道。
#[tauri::command]
pub fn vpn_down_embedded(iface: Option<String>) -> Result<String, String> {
    #[cfg(not(windows))]
    {
        let _ = iface;
        Err("内嵌隧道仅 Windows 使用；Linux 请用 syc down".to_string())
    }
    #[cfg(windows)]
    {
        let _ = iface;
        let exe = std::path::PathBuf::from(r"C:\Program Files\WireGuard\wireguard.exe");
        if !exe.exists() {
            return Err("未找到 wireguard.exe".into());
        }
        let out = Command::new(exe)
            .args(["/uninstalltunnelservice", "syc0"])
            .output()
            .map_err(|e| e.to_string())?;
        Ok(format!("{}", String::from_utf8_lossy(&out.stdout)))
    }
}
