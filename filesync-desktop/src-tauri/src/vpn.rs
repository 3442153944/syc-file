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

use std::process::Command;

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
pub fn vpn_stun(host: String, port: u16) -> Result<String, String> {
    use std::net::UdpSocket;
    use std::time::Duration;
    let sock = UdpSocket::bind("0.0.0.0:0").map_err(|e| e.to_string())?;
    sock.set_read_timeout(Some(Duration::from_secs(3))).map_err(|e| e.to_string())?;
    sock.send_to(b"SYC?", (host.as_str(), port)).map_err(|e| e.to_string())?;
    let mut buf = [0u8; 128];
    let (n, _) = sock.recv_from(&mut buf).map_err(|e| format!("STUN 超时或端口被拦: {e}"))?;
    let reply = String::from_utf8_lossy(&buf[..n]).trim().to_string();
    Ok(reply.strip_prefix("SYC1 ").unwrap_or(&reply).to_string())
}

/// ping 覆盖网地址；iface 指定时强制从隧道网卡发出。
#[tauri::command]
pub fn vpn_ping(target: String, count: Option<u8>, iface: Option<String>) -> Result<String, String> {
    let count = count.unwrap_or(3);
    let mut cmd = Command::new("ping");
    cmd.args(["-c", &count.to_string(), "-W", "2"]);
    if let Some(i) = iface {
        cmd.args(["-I", &i]);
    }
    cmd.arg(&target);
    let out = cmd.output().map_err(|e| format!("执行 ping 失败: {e}"))?;
    let stdout = String::from_utf8_lossy(&out.stdout).to_string();
    if out.status.success() {
        Ok(stdout)
    } else {
        Err(format!("{stdout}{}", String::from_utf8_lossy(&out.stderr)))
    }
}
