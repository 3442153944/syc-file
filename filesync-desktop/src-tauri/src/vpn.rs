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

/// 统一构造子进程：Windows 下加 CREATE_NO_WINDOW，避免 GUI 应用每几秒弹一次黑框。
fn command<P: AsRef<std::ffi::OsStr>>(program: P) -> Command {
    let mut cmd = Command::new(program);
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x0800_0000);
    }
    cmd
}

fn run_syc(args: &[&str]) -> Result<String, String> {
    let out = command("syc")
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
///
/// 必须带 `--wg-port`：STUN 诊断 socket 用的是内核随机端口，它的 NAT 映射不能当
/// WG 端点上报；内嵌隧道在 `vpn_up_embedded` 里固定 `ListenPort = 51821`。
#[tauri::command]
pub fn vpn_nat(device: String) -> Result<String, String> {
    run_syc(&["nat", "--device", &device, "--report", "--wg-port", "51821"])
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
    let mut cmd = command("ping");
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
    let mut cmd = command("ping");
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
        // 直接用 IP Helper API 枚举本机 IPv4，不再起 netsh 子进程。
        use std::net::Ipv4Addr;
        use windows_sys::Win32::NetworkManagement::IpHelper::{
            GetAdaptersAddresses, GAA_FLAG_SKIP_ANYCAST, GAA_FLAG_SKIP_DNS_SERVER,
            GAA_FLAG_SKIP_MULTICAST, IP_ADAPTER_ADDRESSES_LH,
        };
        use windows_sys::Win32::Networking::WinSock::{AF_INET, SOCKADDR_IN};

        let flags = GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST | GAA_FLAG_SKIP_DNS_SERVER;
        let mut size: u32 = 16 * 1024;
        let mut buf: Vec<u8> = vec![0; size as usize];
        let mut addrs = buf.as_mut_ptr() as *mut IP_ADAPTER_ADDRESSES_LH;
        let mut ret = unsafe {
            GetAdaptersAddresses(AF_INET as u32, flags, std::ptr::null_mut(), addrs, &mut size)
        };
        if ret == windows_sys::Win32::Foundation::ERROR_BUFFER_OVERFLOW {
            buf.resize(size as usize, 0);
            addrs = buf.as_mut_ptr() as *mut IP_ADAPTER_ADDRESSES_LH;
            ret = unsafe {
                GetAdaptersAddresses(AF_INET as u32, flags, std::ptr::null_mut(), addrs, &mut size)
            };
        }
        if ret != 0 {
            return Err(format!("GetAdaptersAddresses 失败: {ret}"));
        }
        let mut cur = addrs;
        while !cur.is_null() {
            let mut ua = unsafe { (*cur).FirstUnicastAddress };
            while !ua.is_null() {
                let sa = unsafe { (*ua).Address };
                if !sa.lpSockaddr.is_null()
                    && unsafe { (*sa.lpSockaddr).sa_family } == AF_INET as u16
                {
                    let sin = sa.lpSockaddr as *const SOCKADDR_IN;
                    let raw = unsafe { (*sin).sin_addr.S_un.S_addr };
                    let ip = Ipv4Addr::from(raw.to_ne_bytes());
                    if ip.octets()[0] == 172 && ip.octets()[1] == 1 {
                        return Ok(true);
                    }
                }
                ua = unsafe { (*ua).Next };
            }
            cur = unsafe { (*cur).Next };
        }
        Ok(false)
    }
    #[cfg(not(windows))]
    {
        let out = command("ip").args(["-br", "addr"]).output().map_err(|e| e.to_string())?;
        Ok(String::from_utf8_lossy(&out.stdout).contains("172.1."))
    }
}

#[derive(serde::Deserialize)]
pub struct VpnPeerInput {
    pub pubkey: String,
    pub allowed_ips: Vec<String>,
    pub endpoint: Option<String>,
}

#[tauri::command]
pub fn vpn_mesh_plan(input: sys_core::mesh::Input) -> Vec<sys_core::mesh::Route> {
    sys_core::mesh::plan(&input)
}

#[derive(Default, serde::Serialize)]
pub struct PeerStatus {
    pub pubkey: String,
    pub endpoint: String,
    pub handshake_ms: u64,
    pub allowed_ips: Vec<String>,
}

#[derive(serde::Serialize)]
pub struct InterfaceStatus {
    pub pubkey: String,
    pub listen_port: u16,
    pub memory_backed: bool,
}

/// Public identity only. Never read `showconf`/`dump`, which contain private keys.
#[tauri::command]
pub fn vpn_interface_status(app: tauri::AppHandle) -> Result<InterfaceStatus, String> {
    #[cfg(not(windows))]
    { let _ = app; Err("内嵌隧道仅支持 Windows".into()) }
    #[cfg(windows)]
    {
        let wg = find_wireguard_exe(&app)?.with_file_name("wg.exe");
        let read = |field| -> Result<String, String> {
            let out = command(&wg).args(["show", "syc0", field]).output().map_err(|e| e.to_string())?;
            if !out.status.success() { return Err("无法读取 syc0 的公开身份".into()); }
            Ok(String::from_utf8_lossy(&out.stdout).trim().to_owned())
        };
        let service = command("sc.exe").args(["qc", "WireGuardTunnel$syc0"]).output().map_err(|e| e.to_string())?;
        let memory_backed = service.status.success() && String::from_utf8_lossy(&service.stdout).contains(r"\\.\pipe\filesync-memory-");
        Ok(InterfaceStatus { pubkey: read("public-key")?, listen_port: read("listen-port")?.parse().map_err(|_| "无效的 WireGuard 监听端口")?, memory_backed })
    }
}

#[tauri::command]
pub fn vpn_peer_status(app: tauri::AppHandle) -> Result<Vec<PeerStatus>, String> {
    #[cfg(not(windows))]
    { let _ = app; Err("内嵌隧道仅支持 Windows".into()) }
    #[cfg(windows)]
    {
        let wg = find_wireguard_exe(&app)?.with_file_name("wg.exe");
        let read = |field| -> Result<String, String> {
            let out = command(&wg).args(["show", "syc0", field]).output().map_err(|e| e.to_string())?;
            if !out.status.success() { return Err("无法读取本软件 syc0 隧道状态".into()); }
            Ok(String::from_utf8_lossy(&out.stdout).into_owned())
        };
        let endpoints: std::collections::HashMap<String, String> = read("endpoints")?.lines()
            .filter_map(|line| line.split_once('\t').map(|(k,v)| (k.to_owned(),v.to_owned()))).collect();
        let allowed: std::collections::HashMap<String, Vec<String>> = read("allowed-ips")?.lines()
            .filter_map(|line| line.split_once('\t').map(|(k,v)| (k.to_owned(),v.split(|c: char| c == ',' || c.is_whitespace()).filter(|s| !s.is_empty()).map(str::to_owned).collect()))).collect();
        Ok(read("latest-handshakes")?.lines().filter_map(|line| {
            let (key, time) = line.split_once('\t')?;
            Some(PeerStatus { pubkey: key.into(), endpoint: endpoints.get(key).cloned().unwrap_or_default(),
                allowed_ips: allowed.get(key).cloned().unwrap_or_default(),
                handshake_ms: time.parse::<u64>().unwrap_or(0).saturating_mul(1000) })
        }).collect())
    }
}

/// Update endpoints/ownership without restarting the service or discarding its UDP mapping.
#[tauri::command]
pub fn vpn_update_peers(app: tauri::AppHandle, peers: Vec<VpnPeerInput>) -> Result<(), String> {
    #[cfg(not(windows))]
    { let _ = (app, peers); Err("内嵌隧道仅支持 Windows".into()) }
    #[cfg(windows)]
    {
        let wg = find_wireguard_exe(&app)?.with_file_name("wg.exe");
        let mut config = String::from("[Interface]\n");
        let mut prefixes = Vec::new();
        for peer in peers {
            if peer.pubkey.len() != 44 || !peer.pubkey.bytes().all(|b| b.is_ascii_alphanumeric() || b"+/=".contains(&b)) {
                return Err("无效的 WireGuard 公钥".into());
            }
            for prefix in &peer.allowed_ips {
                let (address, bits) = prefix.split_once('/').ok_or("无效的组网路由")?;
                let address: std::net::Ipv4Addr = address.parse().map_err(|_| "无效的组网地址")?;
                let bits: u8 = bits.parse().map_err(|_| "无效的组网路由前缀")?;
                if address.octets()[..2] != [172, 1] || !(16..=32).contains(&bits) {
                    return Err("路由不在本软件的 172.1.0.0/16 组网范围内".into());
                }
                prefixes.push(prefix.clone());
            }
            config.push_str(&format!("\n[Peer]\nPublicKey = {}\nPersistentKeepalive = 5\n", peer.pubkey));
            if !peer.allowed_ips.is_empty() {
                config.push_str(&format!("AllowedIPs = {}\n", peer.allowed_ips.join(", ")));
            }
            if let Some(endpoint) = peer.endpoint.filter(|e| !e.is_empty()) {
                if endpoint.chars().any(char::is_whitespace) { return Err("无效的组网端点".into()); }
                config.push_str(&format!("Endpoint = {endpoint}\n"));
            }
        }
        let path = std::env::temp_dir().join(format!("filesync-mesh-peers-{}.conf", uuid::Uuid::new_v4()));
        std::fs::write(&path, &config).map_err(|e| e.to_string())?;
        // addconf preserves private key/listen port; syncconf resets omitted
        // Interface fields to zero. Remove obsolete peers explicitly instead.
        let existing = command(&wg).args(["show", "syc0", "peers"]).output().map_err(|e| e.to_string())?;
        if !existing.status.success() { let _ = std::fs::remove_file(&path); return Err("无法读取组网对端".into()); }
        let wanted: std::collections::HashSet<_> = config.lines().filter_map(|line| line.strip_prefix("PublicKey = ")).collect();
        for pubkey in String::from_utf8_lossy(&existing.stdout).split_whitespace() {
            if !wanted.contains(pubkey) {
                let removed = command(&wg).args(["set", "syc0", "peer", pubkey, "remove"]).output().map_err(|e| e.to_string())?;
                if !removed.status.success() { let _ = std::fs::remove_file(&path); return Err("移除组网对端失败".into()); }
            }
        }
        let out = command(&wg).args(["addconf", "syc0"]).arg(&path).output();
        let _ = std::fs::remove_file(&path);
        let out = out.map_err(|e| e.to_string())?;
        if !out.status.success() { return Err(format!("更新隧道对端失败: {}", decode_bytes(&out.stderr))); }
        // active 路由随 syc0 一起消失；netsh 在路由已存在时会失败（例如服务器 172.1.0.0/16
        // 已由隧道服务装好），这属于正常情况。其余失败会让该网段的直连流量没有路由，
        // 必须记录并让调用方下一轮重试，不能像以前那样用 `let _ =` 静默吞掉。
        let mut route_errors: Vec<String> = Vec::new();
        for prefix in prefixes {
            if let Err(e) = add_active_route(&prefix) {
                crate::logger::warn(
                    "vpn",
                    format!("组网路由 {prefix} 添加失败（该网段直连可能不通）: {e}"),
                );
                route_errors.push(format!("{prefix}: {e}"));
            }
        }
        if !route_errors.is_empty() {
            return Err(format!(
                "隧道对端已更新，但组网路由添加失败: {}",
                route_errors.join("; ")
            ));
        }
        Ok(())
    }
}

/// 给 syc0 添加一条「隧道关闭即消失」的 active 路由。
///
/// 返回 Err 只表示真正没加上；「路由已存在」是正常情况（重复同步、服务器 /16 路由），
/// 视为成功。区分这两种情况很重要：前者意味着该网段的直连流量绕不过路由表。
#[cfg(windows)]
fn add_active_route(prefix: &str) -> Result<(), String> {
    let out = command("netsh")
        .args([
            "interface",
            "ipv4",
            "add",
            "route",
            &format!("prefix={prefix}"),
            "interface=syc0",
            "store=active",
        ])
        .output()
        .map_err(|e| format!("执行 netsh 失败: {e}"))?;
    if out.status.success() {
        return Ok(());
    }
    // netsh 的错误输出跟随系统语言：英文 "The object already exists."、中文「对象已存在。」
    let detail = format!("{}{}", decode_bytes(&out.stdout), decode_bytes(&out.stderr));
    let detail = detail.trim();
    if detail.to_ascii_lowercase().contains("already exists") || detail.contains("已存在") {
        return Ok(());
    }
    Err(if detail.is_empty() {
        format!("netsh 退出码 {:?}", out.status.code())
    } else {
        detail.to_string()
    })
}

#[tauri::command]
pub fn vpn_lan_endpoints(cidrs: Option<bool>) -> Result<Vec<String>, String> {
    #[cfg(not(windows))]
    { let _ = cidrs; Ok(Vec::new()) }
    #[cfg(windows)]
    {
        use windows_sys::Win32::NetworkManagement::IpHelper::{GetAdaptersAddresses, IP_ADAPTER_ADDRESSES_LH};
        use windows_sys::Win32::Networking::WinSock::{AF_INET, SOCKADDR_IN};
        let mut size = 16 * 1024;
        let mut buffer = vec![0u8; size as usize];
        let mut result = unsafe { GetAdaptersAddresses(AF_INET as u32, 0, std::ptr::null_mut(), buffer.as_mut_ptr().cast(), &mut size) };
        if result == windows_sys::Win32::Foundation::ERROR_BUFFER_OVERFLOW {
            buffer.resize(size as usize, 0);
            result = unsafe { GetAdaptersAddresses(AF_INET as u32, 0, std::ptr::null_mut(), buffer.as_mut_ptr().cast(), &mut size) };
        }
        if result != 0 { return Err(format!("GetAdaptersAddresses: {result}")); }
        let mut addresses = Vec::new();
        let mut adapter = buffer.as_mut_ptr().cast::<IP_ADAPTER_ADDRESSES_LH>();
        while !adapter.is_null() {
            if unsafe { (*adapter).OperStatus } != 1 { adapter = unsafe { (*adapter).Next }; continue; }
            let mut entry = unsafe { (*adapter).FirstUnicastAddress };
            while !entry.is_null() {
                let socket = unsafe { (*entry).Address.lpSockaddr };
                if !socket.is_null() && unsafe { (*socket).sa_family } == AF_INET as u16 {
                    let raw = unsafe { (*(socket as *const SOCKADDR_IN)).sin_addr.S_un.S_addr };
                    let ip = std::net::Ipv4Addr::from(raw.to_ne_bytes());
                    if ip.is_private() { addresses.push(if cidrs.unwrap_or(false) { format!("{ip}/{}", unsafe { (*entry).OnLinkPrefixLength }) } else { format!("{ip}:51821") }); }
                }
                entry = unsafe { (*entry).Next };
            }
            adapter = unsafe { (*adapter).Next };
        }
        addresses.sort(); addresses.dedup(); addresses.truncate(8);
        Ok(addresses)
    }
}

/// 用内嵌/已安装的 WireGuard for Windows 启动隧道。
/// 配置经一次性受限命名管道交给系统服务；私钥不写磁盘。
#[tauri::command]
pub fn vpn_up_embedded(
    app: tauri::AppHandle,
    device: String,
    private_key: String,
    public_key: String,
    address: String,
    subnet: String,
    keepalive: Option<u32>,
    peers: Vec<VpnPeerInput>,
) -> Result<String, String> {
    #[cfg(not(windows))]
    {
        let _ = (app, device, private_key, public_key, address, subnet, keepalive, peers);
        Err("内嵌隧道仅 Windows 使用；Linux 请用 syc up".to_string())
    }
    #[cfg(windows)]
    {
        let key = private_key;
        let bits = subnet.rsplit('/').next().unwrap_or("24");
        let ka = keepalive.unwrap_or(5);
        let mut conf = String::new();
        conf.push_str("[Interface]\n");
        conf.push_str(&format!("PrivateKey = {key}\n"));
        conf.push_str(&format!("Address = {address}/{bits}\n"));
        conf.push_str("MTU = 1280\n");
        // 固定客户端监听端口：对端才能用「本机 IP:51821」直连；
        // 随机端口时只有服务器 wg show 能看到，无法通过心跳/请求头上报。
        conf.push_str("ListenPort = 51821\n\n");
        for p in peers {
            conf.push_str("[Peer]\n");
            conf.push_str(&format!("PublicKey = {}\n", p.pubkey));
            if !p.allowed_ips.is_empty() {
                conf.push_str(&format!("AllowedIPs = {}\n", p.allowed_ips.join(", ")));
            }
            if let Some(ep) = p.endpoint.filter(|e| !e.trim().is_empty()) {
                conf.push_str(&format!("Endpoint = {ep}\n"));
            }
            conf.push_str(&format!("PersistentKeepalive = {ka}\n\n"));
        }
        let exe = find_wireguard_exe(&app)?;
        // Replace only our tunnel after authenticated identity retrieval.
        let _ = command(&exe).args(["/uninstalltunnelservice", "syc0"]).output();
        let legacy = std::path::PathBuf::from(std::env::var("ProgramData").unwrap_or_else(|_| "C:\\ProgramData".into()))
            .join("syc").join("syc0.conf");
        match std::fs::remove_file(legacy) {
            Ok(()) => (),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err("无法清理旧版组网配置，请以管理员权限重试".into()),
        }
        sys_core::windows_tunnel::install_memory_tunnel(&exe, "syc0", &public_key, &conf)?;
        Ok("隧道已启动（密钥仅驻内存）".into())
    }
}

/// 候选路径（按优先级）：安装包布局 → 绿色包/旧布局 → 开发模式仓库副本。
///
/// Tauri v2 会把 `bundle.resources` 里的 `resources/wireguard/*` 原样放到
/// `<resource_dir>/resources/wireguard/`；Windows 上 `resource_dir` 就是 exe 所在目录，
/// 所以安装后实际路径是 `<安装目录>\resources\wireguard\wireguard.exe`。
fn wireguard_candidates(
    resource_dir: Option<&std::path::Path>,
    exe_dir: Option<&std::path::Path>,
    manifest_dir: Option<&std::path::Path>,
) -> Vec<std::path::PathBuf> {
    let mut candidates = Vec::new();
    for base in [resource_dir, exe_dir].into_iter().flatten() {
        candidates.push(base.join("resources").join("wireguard").join("wireguard.exe"));
        // 兼容手工绿色包 / 早期布局：<目录>\wireguard\wireguard.exe
        candidates.push(base.join("wireguard").join("wireguard.exe"));
    }
    // 仓库内副本只在 debug 构建兜底，release 不能依赖编译机上的源码目录。
    if let Some(dir) = manifest_dir {
        candidates.push(dir.join("resources").join("wireguard").join("wireguard.exe"));
    }
    // Windows 上 resource_dir 与 exe 目录相同，去重后错误信息里不会重复同一路径。
    let mut seen = std::collections::HashSet::new();
    candidates.retain(|p| seen.insert(p.clone()));
    candidates
}

#[cfg(windows)]
fn find_wireguard_exe(app: &tauri::AppHandle) -> Result<std::path::PathBuf, String> {
    // 只使用自依赖：安装后用随包分发的副本，开发模式用仓库内那份。
    // 刻意不回退到 Program Files，避免和用户自己安装/运行的 WireGuard 混用。
    let resource_dir = app.path().resource_dir().ok();
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|exe| exe.parent().map(std::path::Path::to_path_buf));
    // debug 构建（cargo run / tauri dev 未拷贝资源时）才回退到仓库目录；
    // 安装版是 release 构建，不能把编译机的绝对路径当成资源位置。
    let manifest_dir =
        cfg!(debug_assertions).then(|| std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")));
    let candidates =
        wireguard_candidates(resource_dir.as_deref(), exe_dir.as_deref(), manifest_dir.as_deref());
    candidates
        .iter()
        .find(|p| p.exists())
        .cloned()
        .ok_or_else(|| {
            let searched = candidates
                .iter()
                .map(|p| format!("  - {}", p.display()))
                .collect::<Vec<_>>()
                .join("\n");
            format!(
                "未找到内置 wireguard.exe：请确认 resources/wireguard/ 随包分发（开发模式检查 src-tauri/resources/wireguard/）。已查找：\n{searched}"
            )
        })
}

/// 关闭内嵌隧道。
#[tauri::command]
pub async fn vpn_down_embedded(
    app: tauri::AppHandle,
    state: tauri::State<'_, crate::mesh_proxy_commands::MeshProxyState>,
    iface: Option<String>,
) -> Result<String, String> {
    crate::mesh_proxy_commands::mesh_proxy_stop(app.clone(), state).await?;
    #[cfg(not(windows))]
    {
        let _ = iface;
        Err("内嵌隧道仅 Windows 使用；Linux 请用 syc down".to_string())
    }
    #[cfg(windows)]
    {
        let _ = iface;
        let exe = find_wireguard_exe(&app)?;
        let out = tauri::async_runtime::spawn_blocking(move || {
            command(exe)
                .args(["/uninstalltunnelservice", "syc0"])
                .output()
        })
        .await
        .map_err(|e| e.to_string())?
        .map_err(|e| e.to_string())?;
        if !out.status.success() {
            return Err(format!("停止隧道失败: {}{}", decode_bytes(&out.stdout), decode_bytes(&out.stderr)));
        }
        Ok(format!("{}", String::from_utf8_lossy(&out.stdout)))
    }
}

/// 开机自启：Windows 写注册表 Run，Linux 写 ~/.config/autostart/*.desktop，由插件分别处理。
#[tauri::command]
pub fn autostart_get(app: tauri::AppHandle) -> Result<bool, String> {
    use tauri_plugin_autostart::ManagerExt;
    app.autolaunch().is_enabled().map_err(|e| e.to_string())
}

#[tauri::command]
pub fn autostart_set(app: tauri::AppHandle, enabled: bool) -> Result<(), String> {
    use tauri_plugin_autostart::ManagerExt;
    let mgr = app.autolaunch();
    if enabled {
        mgr.enable().map_err(|e| e.to_string())
    } else {
        mgr.disable().map_err(|e| e.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::wireguard_candidates;
    use std::path::PathBuf;

    #[test]
    fn prefers_bundled_resource_layout() {
        // 安装版布局：<安装目录>\resources\wireguard\wireguard.exe
        let install = PathBuf::from("install");
        let candidates = wireguard_candidates(Some(&install), None, None);
        let expected = install.join("resources").join("wireguard").join("wireguard.exe");
        assert_eq!(candidates.first(), Some(&expected));
    }

    #[test]
    fn keeps_legacy_and_dev_fallbacks() {
        let install = PathBuf::from("install");
        let repo = PathBuf::from("repo");
        let candidates = wireguard_candidates(Some(&install), None, Some(&repo));
        assert!(candidates.contains(&install.join("wireguard").join("wireguard.exe")));
        assert!(candidates.contains(&repo.join("resources").join("wireguard").join("wireguard.exe")));
    }

    #[test]
    fn dedupes_resource_and_exe_dir() {
        // Windows 上 resource_dir 与 exe 目录通常相同，重复候选不影响结果。
        let install = PathBuf::from("install");
        let candidates = wireguard_candidates(Some(&install), Some(&install), None);
        assert_eq!(candidates.len(), 2);
        assert_eq!(candidates[0], install.join("resources").join("wireguard").join("wireguard.exe"));
    }
}
