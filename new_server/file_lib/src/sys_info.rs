//! 系统明细采集：进程 Top-N（CPU/内存/磁盘 IO + 网络连接数）、监听端口、
//! 端口连接数统计。CPU/内存/磁盘走 `sysinfo`（跨平台）；端口/连接走 `netstat2`
//! （Windows 是 GetExtendedTcpTable/UdpTable，Linux 是 procfs，内部已抹平）。
//!
//! 采集节奏（多久采一次）由 Go 侧的 ticker 控制，这里只管"采一次"；
//! 唯一需要跨调用保留的状态是 `sysinfo::System`——它的 cpu_usage/disk_usage
//! 语义是"相对上一次 refresh 的增量"，采集间隔越久这次的读数就覆盖越久。
//!
//! 网络维度缺一个跨平台可靠的"进程级字节数"接口（Windows 要吃 ETW、Linux 要
//! eBPF/cgroup 记账，代价和这个功能的定位不成比例），综合评分里的"网络"用
//! 该进程持有的 TCP/UDP 连接数作代理指标——连接数本来就是端口维度需要采的
//! 同一份数据，顺带算进评分不用再多扫一遍。

use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};

use netstat2::{
    get_sockets_info, AddressFamilyFlags, ProtocolFlags, ProtocolSocketInfo, TcpState,
};
use serde::Serialize;
use sysinfo::System;

#[derive(Serialize)]
pub struct ProcessInfo {
    pub pid: u32,
    pub name: String,
    pub cpu_percent: f32,
    pub mem_bytes: u64,
    pub mem_percent: f32,
    pub disk_read_bytes: u64,
    pub disk_write_bytes: u64,
    pub connections: u32,
    /// 排序用的综合评分：2*cpu 占比 + 1.5*mem 占比 + 1*连接数占比（各自在本轮
    /// 采样的进程集合内做 0~1 归一化，量纲不同没法直接比，归一化后才能加权）。
    pub score: f32,
}

#[derive(Serialize)]
pub struct ListeningPort {
    pub port: u16,
    pub protocol: &'static str,
    pub pid: u32,
    pub process_name: String,
}

#[derive(Serialize)]
pub struct PortConnCount {
    pub port: u16,
    pub protocol: &'static str,
    pub connections: u32,
}

#[derive(Serialize)]
pub struct Snapshot {
    pub processes: Vec<ProcessInfo>,
    pub listening_ports: Vec<ListeningPort>,
    pub port_connections: Vec<PortConnCount>,
}

fn system() -> &'static Mutex<System> {
    static SYS: OnceLock<Mutex<System>> = OnceLock::new();
    SYS.get_or_init(|| Mutex::new(System::new_all()))
}

/// 采一次快照，序列化成 JSON 字符串（失败时退化成 `{}`，调用方按空快照处理）。
pub fn collect_snapshot(top_n: usize) -> String {
    serde_json::to_string(&build_snapshot(top_n)).unwrap_or_else(|_| "{}".to_string())
}

fn build_snapshot(top_n: usize) -> Snapshot {
    let mut sys = system().lock().unwrap_or_else(|e| e.into_inner());
    sys.refresh_all();

    let mut names: HashMap<u32, String> = HashMap::new();
    for (pid, p) in sys.processes() {
        names.insert(pid.as_u32(), p.name().to_string_lossy().to_string());
    }

    let (listening_ports, port_connections, conn_by_pid) = scan_sockets(&names);

    let total_mem = sys.total_memory().max(1) as f32;
    let max_cpu = sys
        .processes()
        .values()
        .map(|p| p.cpu_usage())
        .fold(0.0f32, f32::max)
        .max(1e-6);
    let max_conn = conn_by_pid.values().copied().max().unwrap_or(0).max(1) as f32;

    let mut processes: Vec<ProcessInfo> = sys
        .processes()
        .iter()
        .map(|(pid, p)| {
            let pid_u32 = pid.as_u32();
            let cpu = p.cpu_usage();
            let mem_bytes = p.memory();
            let mem_percent = mem_bytes as f32 / total_mem * 100.0;
            let disk = p.disk_usage();
            let conn = conn_by_pid.get(&pid_u32).copied().unwrap_or(0);
            let score = 2.0 * (cpu / max_cpu)
                + 1.5 * (mem_percent / 100.0).min(1.0)
                + 1.0 * (conn as f32 / max_conn);
            ProcessInfo {
                pid: pid_u32,
                name: p.name().to_string_lossy().to_string(),
                cpu_percent: cpu,
                mem_bytes,
                mem_percent,
                disk_read_bytes: disk.read_bytes,
                disk_write_bytes: disk.written_bytes,
                connections: conn,
                score,
            }
        })
        .collect();

    processes.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
    processes.truncate(top_n);

    Snapshot {
        processes,
        listening_ports,
        port_connections,
    }
}

type ConnByPid = HashMap<u32, u32>;

/// 扫一遍 TCP/UDP 套接字：监听中的端口列表、按端口聚合的连接数、按 pid 聚合的
/// 连接数（喂给上面的综合评分）。一次扫描三份都要的数据一起产出，不重复扫。
fn scan_sockets(names: &HashMap<u32, String>) -> (Vec<ListeningPort>, Vec<PortConnCount>, ConnByPid) {
    let af = AddressFamilyFlags::IPV4 | AddressFamilyFlags::IPV6;
    let proto = ProtocolFlags::TCP | ProtocolFlags::UDP;
    let sockets = get_sockets_info(af, proto).unwrap_or_default();

    let mut listening = Vec::new();
    let mut conn_count: HashMap<(u16, &'static str), u32> = HashMap::new();
    let mut conn_by_pid: ConnByPid = HashMap::new();

    for info in sockets {
        let pids = info.associated_pids.clone();
        match info.protocol_socket_info {
            ProtocolSocketInfo::Tcp(tcp) => {
                *conn_count.entry((tcp.local_port, "tcp")).or_insert(0) += 1;
                for pid in &pids {
                    *conn_by_pid.entry(*pid).or_insert(0) += 1;
                }
                if tcp.state == TcpState::Listen {
                    if let Some(pid) = pids.first() {
                        listening.push(ListeningPort {
                            port: tcp.local_port,
                            protocol: "tcp",
                            pid: *pid,
                            process_name: names.get(pid).cloned().unwrap_or_default(),
                        });
                    }
                }
            }
            ProtocolSocketInfo::Udp(udp) => {
                *conn_count.entry((udp.local_port, "udp")).or_insert(0) += 1;
                for pid in &pids {
                    *conn_by_pid.entry(*pid).or_insert(0) += 1;
                }
                // UDP 无连接状态，绑定了本地端口就算"在监听"
                if let Some(pid) = pids.first() {
                    listening.push(ListeningPort {
                        port: udp.local_port,
                        protocol: "udp",
                        pid: *pid,
                        process_name: names.get(pid).cloned().unwrap_or_default(),
                    });
                }
            }
        }
    }

    let port_connections = conn_count
        .into_iter()
        .map(|((port, protocol), connections)| PortConnCount {
            port,
            protocol,
            connections,
        })
        .collect();

    (listening, port_connections, conn_by_pid)
}
