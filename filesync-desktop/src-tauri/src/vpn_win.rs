//! Windows 组网出口管理：枚举物理网卡，并把隧道外层流量（对端/中继服务器）固定到
//! 用户选择的物理网卡，避免被系统里其它 VPN/Clash 抢走默认路由、把"直连"变成绕行中继。
//!
//! 非 Windows 平台编译为空实现：桌面端目前只在 Windows 上做组网隧道。
use serde::Serialize;

#[derive(Clone, Serialize)]
pub struct PhysicalInterface {
    pub name: String,
    pub description: String,
    pub if_index: u32,
    pub addresses: Vec<String>,
    pub gateways: Vec<String>,
}

/// 可供"组网出口网卡"选择的物理网卡（不含 WireGuard/TAP/Tailscale/Clash 等虚拟网卡）。
#[tauri::command]
pub fn vpn_physical_interfaces() -> Result<Vec<PhysicalInterface>, String> {
    #[cfg(not(windows))]
    {
        Ok(Vec::new())
    }
    #[cfg(windows)]
    {
        imp::physical_interfaces()
    }
}

/// 选择组网外层流量的出口网卡：`None` 表示跟随系统路由；`Some(if_index)` 表示强制走该物理网卡。
/// 选择后会立即把当前所有隧道端点的 /32 主机路由固定到该网卡。
#[tauri::command]
pub fn vpn_egress_set(app: tauri::AppHandle, if_index: Option<u32>) -> Result<(), String> {
    #[cfg(not(windows))]
    {
        let _ = (app, if_index);
        Err("仅 Windows 支持指定组网出口网卡".into())
    }
    #[cfg(windows)]
    {
        imp::set_egress_and_apply(&app, if_index)
    }
}

#[cfg(windows)]
pub(crate) use imp::{apply_routes_from_wg, clear_egress_routes};

#[cfg(windows)]
mod imp {
    use super::PhysicalInterface;
    use std::{
        collections::HashSet,
        net::Ipv4Addr,
        os::windows::process::CommandExt,
        path::Path,
        process::{Command, Output, Stdio},
        ptr,
        sync::Mutex,
    };
    use windows_sys::Win32::{
        Foundation::ERROR_BUFFER_OVERFLOW,
        NetworkManagement::IpHelper::{GetAdaptersAddresses, IP_ADAPTER_ADDRESSES_LH},
        Networking::WinSock::{AF_INET, SOCKADDR, SOCKADDR_IN},
    };

    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    // GAA_FLAG_INCLUDE_GATEWAYS：枚举网卡时一并返回默认网关。
    const GAA_FLAG_INCLUDE_GATEWAYS: u32 = 0x0080;

    struct Adapter {
        name: String,
        description: String,
        if_index: u32,
        if_type: u32,
        up: bool,
        addresses: Vec<(Ipv4Addr, u8)>,
        gateways: Vec<Ipv4Addr>,
    }

    fn pwstr(value: *const u16) -> String {
        if value.is_null() {
            return String::new();
        }
        let mut len = 0usize;
        unsafe {
            while *value.add(len) != 0 {
                len += 1;
            }
            String::from_utf16_lossy(std::slice::from_raw_parts(value, len))
        }
    }

    fn sockaddr_ipv4(address: &SOCKADDR) -> Option<Ipv4Addr> {
        if address.sa_family != AF_INET as u16 {
            return None;
        }
        let raw = unsafe { (*(address as *const SOCKADDR as *const SOCKADDR_IN)).sin_addr.S_un.S_addr };
        Some(Ipv4Addr::from(raw.to_ne_bytes()))
    }

    fn adapters() -> Result<Vec<Adapter>, String> {
        let mut size: u32 = 16 * 1024;
        let mut buffer = vec![0u8; size as usize];
        let mut result = unsafe {
            GetAdaptersAddresses(
                AF_INET as u32,
                GAA_FLAG_INCLUDE_GATEWAYS,
                ptr::null_mut(),
                buffer.as_mut_ptr().cast(),
                &mut size,
            )
        };
        if result == ERROR_BUFFER_OVERFLOW {
            buffer.resize(size as usize, 0);
            result = unsafe {
                GetAdaptersAddresses(
                    AF_INET as u32,
                    GAA_FLAG_INCLUDE_GATEWAYS,
                    ptr::null_mut(),
                    buffer.as_mut_ptr().cast(),
                    &mut size,
                )
            };
        }
        if result != 0 {
            return Err(format!("枚举网卡失败（错误代码 {result}）"));
        }
        let mut list = Vec::new();
        let mut adapter = buffer.as_mut_ptr().cast::<IP_ADAPTER_ADDRESSES_LH>();
        while !adapter.is_null() {
            let info = unsafe { &*adapter };
            let mut addresses = Vec::new();
            let mut unicast = info.FirstUnicastAddress;
            while !unicast.is_null() {
                let entry = unsafe { &*unicast };
                if !entry.Address.lpSockaddr.is_null() {
                    if let Some(address) = sockaddr_ipv4(unsafe { &*entry.Address.lpSockaddr }) {
                        addresses.push((address, entry.OnLinkPrefixLength));
                    }
                }
                unicast = entry.Next;
            }
            let mut gateways = Vec::new();
            let mut gateway = info.FirstGatewayAddress;
            while !gateway.is_null() {
                let entry = unsafe { &*gateway };
                if !entry.Address.lpSockaddr.is_null() {
                    if let Some(address) = sockaddr_ipv4(unsafe { &*entry.Address.lpSockaddr }) {
                        gateways.push(address);
                    }
                }
                gateway = entry.Next;
            }
            list.push(Adapter {
                name: pwstr(info.FriendlyName),
                description: pwstr(info.Description),
                if_index: unsafe { info.Anonymous1.Anonymous.IfIndex },
                if_type: info.IfType,
                up: info.OperStatus == 1,
                addresses,
                gateways,
            });
            adapter = info.Next;
        }
        Ok(list)
    }

    fn is_physical(adapter: &Adapter) -> bool {
        // IANA ifType：6=ethernet、71=ieee80211、53=propVirtual、131=tunnel。
        // WireGuard/Tailscale/Mihomo 报 53，TAP 报 6，所以名字关键词也要排除。
        const IF_TYPE_ETHERNET_CSMACD: u32 = 6;
        const IF_TYPE_IEEE80211: u32 = 71;
        let text = format!("{} {}", adapter.name, adapter.description).to_ascii_lowercase();
        let virtual_keyword = [
            "wireguard", "wintun", "tunnel", "tap", "tun", "vpn", "tailscale", "zerotier",
            "mihomo", "clash", "virtual", "vmware", "hyper-v", "vethernet", "loopback",
        ]
        .iter()
        .any(|keyword| text.contains(keyword));
        (adapter.if_type == IF_TYPE_ETHERNET_CSMACD || adapter.if_type == IF_TYPE_IEEE80211)
            && !virtual_keyword
    }

    pub(super) fn physical_interfaces() -> Result<Vec<PhysicalInterface>, String> {
        Ok(adapters()?
            .into_iter()
            .filter(|adapter| adapter.up && is_physical(adapter) && !adapter.addresses.is_empty())
            .map(|adapter| PhysicalInterface {
                name: adapter.name,
                description: adapter.description,
                if_index: adapter.if_index,
                addresses: adapter
                    .addresses
                    .iter()
                    .map(|(address, prefix)| format!("{address}/{prefix}"))
                    .collect(),
                gateways: adapter.gateways.iter().map(ToString::to_string).collect(),
            })
            .collect())
    }

    #[derive(Clone)]
    struct Egress {
        if_index: u32,
        name: String,
        addresses: Vec<(Ipv4Addr, u8)>,
        gateway: Option<Ipv4Addr>,
    }

    static EGRESS: Mutex<Option<Egress>> = Mutex::new(None);
    static ROUTES: Mutex<Vec<(Ipv4Addr, u32)>> = Mutex::new(Vec::new());

    pub(super) fn set_egress_and_apply(
        app: &tauri::AppHandle,
        if_index: Option<u32>,
    ) -> Result<(), String> {
        clear_egress_routes();
        {
            let mut slot = EGRESS.lock().map_err(|_| "出口网卡状态不可用".to_string())?;
            *slot = None;
        }
        let Some(index) = if_index else { return Ok(()) };
        let adapter = adapters()?
            .into_iter()
            .find(|adapter| adapter.if_index == index)
            .ok_or_else(|| format!("未找到网卡索引 {index}"))?;
        if !is_physical(&adapter) {
            return Err(format!("{} 不是物理网卡，不能作为组网出口", adapter.name));
        }
        {
            let mut slot = EGRESS.lock().map_err(|_| "出口网卡状态不可用".to_string())?;
            *slot = Some(Egress {
                if_index: index,
                name: adapter.name,
                addresses: adapter.addresses,
                gateway: adapter.gateways.first().copied(),
            });
        }
        let wg = crate::vpn::find_wireguard_exe(app)?.with_file_name("wg.exe");
        apply_routes_from_wg(&wg)
    }

    pub(crate) fn apply_routes_from_wg(wg: &Path) -> Result<(), String> {
        let out = hidden(wg)
            .args(["show", "syc0", "endpoints"])
            .output()
            .map_err(|error| format!("读取隧道端点失败: {error}"))?;
        if !out.status.success() {
            return Err("读取隧道端点失败".into());
        }
        let endpoints: Vec<Ipv4Addr> = String::from_utf8_lossy(&out.stdout)
            .lines()
            .filter_map(|line| line.split_once('\t').map(|(_, value)| value.trim()))
            .filter_map(endpoint_ipv4)
            .collect();
        apply_routes(&endpoints)
    }

    fn apply_routes(endpoints: &[Ipv4Addr]) -> Result<(), String> {
        let egress = {
            let slot = EGRESS.lock().map_err(|_| "出口网卡状态不可用".to_string())?;
            slot.clone()
        };
        let Some(egress) = egress else {
            clear_egress_routes();
            return Ok(());
        };
        let desired: HashSet<Ipv4Addr> = endpoints.iter().copied().collect();
        let mut current = {
            let mut routes = ROUTES.lock().map_err(|_| "出口路由状态不可用".to_string())?;
            routes.retain(|(address, if_index)| {
                if desired.contains(address) {
                    true
                } else {
                    delete_route(*address, *if_index);
                    false
                }
            });
            routes.clone()
        };
        let mut errors = Vec::new();
        for address in desired {
            if current.iter().any(|(existing, _)| *existing == address) {
                continue;
            }
            match add_route(address, &egress) {
                Ok(()) => current.push((address, egress.if_index)),
                Err(error) => errors.push(error),
            }
        }
        if let Ok(mut routes) = ROUTES.lock() {
            *routes = current;
        }
        if errors.is_empty() {
            Ok(())
        } else {
            Err(errors.join("; "))
        }
    }

    pub(crate) fn clear_egress_routes() {
        let drained = {
            let Ok(mut routes) = ROUTES.lock() else { return };
            routes.drain(..).collect::<Vec<_>>()
        };
        for (address, if_index) in drained {
            delete_route(address, if_index);
        }
    }

    fn route_next_hop(egress: &Egress, address: Ipv4Addr) -> Result<Option<Ipv4Addr>, String> {
        if egress
            .addresses
            .iter()
            .any(|(network, prefix)| prefix_matches(*network, *prefix, address))
        {
            return Ok(None); // 同网段 on-link
        }
        egress.gateway.map(Some).ok_or_else(|| {
            format!(
                "出口网卡 {} 没有默认网关，无法把 {} 固定到该网卡",
                egress.name, address
            )
        })
    }

    fn add_route(address: Ipv4Addr, egress: &Egress) -> Result<(), String> {
        let next_hop = route_next_hop(egress, address)?;
        let prefix = format!("{address}/32");
        let run = |operation: &str| -> Result<Output, String> {
            let mut args = vec![
                "interface".to_string(),
                "ipv4".to_string(),
                operation.to_string(),
                "route".to_string(),
                format!("prefix={prefix}"),
                format!("interface={}", egress.if_index),
                "store=active".to_string(),
            ];
            if operation == "add" {
                if let Some(gateway) = next_hop {
                    args.push(format!("nexthop={gateway}"));
                }
                args.push("metric=1".to_string());
            }
            netsh(&args)
        };
        let first = run("add")?;
        if first.status.success() {
            return Ok(());
        }
        // netsh 输出是本地化的，不解析文本：删掉重加一次即是幂等语义。
        let _ = run("delete");
        let retry = run("add")?;
        if retry.status.success() {
            return Ok(());
        }
        Err(format!(
            "为 {address} 添加出口路由失败（netsh 退出码 {:?}）",
            retry.status.code()
        ))
    }

    fn delete_route(address: Ipv4Addr, if_index: u32) {
        let _ = netsh(&[
            "interface".to_string(),
            "ipv4".to_string(),
            "delete".to_string(),
            "route".to_string(),
            format!("prefix={address}/32"),
            format!("interface={if_index}"),
            "store=active".to_string(),
        ]);
    }

    fn netsh(args: &[String]) -> Result<Output, String> {
        hidden(Path::new("netsh"))
            .args(args)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .output()
            .map_err(|error| format!("执行 netsh 失败: {error}"))
    }

    fn hidden(program: &Path) -> Command {
        let mut command = Command::new(program);
        command.creation_flags(CREATE_NO_WINDOW);
        command
    }

    fn endpoint_ipv4(endpoint: &str) -> Option<Ipv4Addr> {
        let value = endpoint.trim();
        if let Ok(address) = value.parse() {
            return Some(address);
        }
        let host = if let Some(rest) = value.strip_prefix('[') {
            rest.split(']').next()?
        } else {
            value.rsplit_once(':').map(|(host, _)| host).unwrap_or(value)
        };
        host.parse().ok()
    }

    fn prefix_matches(network: Ipv4Addr, prefix: u8, address: Ipv4Addr) -> bool {
        if prefix == 0 {
            return true;
        }
        let mask = if prefix >= 32 { u32::MAX } else { u32::MAX << (32 - prefix) };
        u32::from_be_bytes(network.octets()) & mask == u32::from_be_bytes(address.octets()) & mask
    }
}
