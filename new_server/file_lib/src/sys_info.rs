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
use std::time::{Duration, Instant};

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
    /// 本轮采集用来归一化 cpu_percent 的逻辑核数，跟着快照带出去方便核对
    /// （比如怀疑某台机器上算出来的 cpu_percent 不对头，先看这个数对不对）。
    pub num_cpus: u32,
}

/// 长期存活的 System 会在 Windows 上偶发"某个 PID 的 cpu_usage 卡死不再更新"
/// （排查记录：一个已经退出的 rustc.exe 子进程，读数精确冻结在同一个值上，
/// 跨越采集间隔从 1s 变到 30s 都没有任何变化——sysinfo 对它的增量追踪状态
/// 没有正常收敛到 0，只是不再刷新了。具体是 sysinfo 在 Windows 下哪一步的
/// 边界情况目前没有继续深挖，但现象很明确：只要 System 实例活得够久，
/// 就可能有 PID 卡在这种状态里，从"僵尸读数"变成"永远的僵尸读数"）。
///
/// 兜底方案：定期把整个 System 换成全新实例。新实例里没有任何 PID 的历史
/// 基线，卡死的旧状态没机会带过来。代价是换新那一轮 cpu_usage 会短暂失真
/// （denominator 太小），但这比一个进程的读数永远卡死要好得多。
const SYS_RESET_INTERVAL: Duration = Duration::from_secs(120);

struct SysState {
    sys: System,
    last_reset: Instant,
}

fn system() -> &'static Mutex<SysState> {
    static SYS: OnceLock<Mutex<SysState>> = OnceLock::new();
    SYS.get_or_init(|| {
        Mutex::new(SysState {
            sys: System::new_all(),
            last_reset: Instant::now(),
        })
    })
}

/// 采一次快照，序列化成 JSON 字符串（失败时退化成 `{}`，调用方按空快照处理）。
pub fn collect_snapshot(top_n: usize) -> String {
    serde_json::to_string(&build_snapshot(top_n)).unwrap_or_else(|_| "{}".to_string())
}

fn build_snapshot(top_n: usize) -> Snapshot {
    let mut state = system().lock().unwrap_or_else(|e| e.into_inner());
    if state.last_reset.elapsed() >= SYS_RESET_INTERVAL {
        state.sys = System::new_all();
        state.last_reset = Instant::now();
    }
    let sys = &mut state.sys;
    sys.refresh_all();

    let mut names: HashMap<u32, String> = HashMap::new();
    for (pid, p) in sys.processes() {
        names.insert(pid.as_u32(), p.name().to_string_lossy().to_string());
    }

    let (listening_ports, port_connections, conn_by_pid) = scan_sockets(&names);

    let total_mem = sys.total_memory().max(1) as f32;
    // sysinfo 的 Process::cpu_usage() 是相对单核的：一个吃满 4 个核的进程在 8 核机器上
    // 报的是 400，不是 50。不除以核数直接当"占用百分比"用会跟前端 0~100% 的坐标轴、
    // 以及"总 CPU 占用"这类直觉严重对不上（尤其多线程进程一冒峰就顶穿 100% 刻度线）。
    // 这里统一换算成相对整机的占比，跟 mem_percent 口径一致。
    let num_cpus = sys.cpus().len().max(1) as f32;
    let max_cpu = sys
        .processes()
        .values()
        .map(|p| p.cpu_usage() / num_cpus)
        .fold(0.0f32, f32::max)
        .max(1e-6);
    let max_conn = conn_by_pid.values().copied().max().unwrap_or(0).max(1) as f32;

    let mut processes: Vec<ProcessInfo> = sys
        .processes()
        .iter()
        .map(|(pid, p)| {
            let pid_u32 = pid.as_u32();
            // 单个进程物理上不可能用得比"整机所有核"还多，超过 100 只可能是 sysinfo
            // 在采样窗口内的测量噪声（线程刚创建/刚被调度到不同核之类），夹住兜底。
            let cpu = (p.cpu_usage() / num_cpus).min(100.0);
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
        num_cpus: num_cpus as u32,
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
