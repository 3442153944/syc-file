//! 多路径上传决策核心：测速编排 → 同后端校验 → 父链折算 → 选片/初始窗口 → AIMD 调度。
//!
//! 职责划分：本模块只做决策，不发任何网络请求。平台（桌面进程内 / Android JNI /
//! 未来鸿蒙 NAPI）循环 `next_task()` 拿到任务、执行 HTTP 发送、把结果喂回 `report()`，
//! 直到 `Task::Done` / `Task::Failed`：
//!
//! ```text
//! let mut p = Planner::new(input)?;            // 传入节点池 + 上次持久化状态
//! loop {
//!     match p.next_task() {
//!         Task::Probe{t} => { let r = platform_probe(t); p.report(r); }
//!         Task::Chunk{t} => { let r = platform_send(t);  p.report(r); }
//!         Task::Reinit   => { let (id, missing) = platform_reinit(); p.resume(id, missing); }
//!         Task::Wait     => { /* 等某个在途分片的超时/结果驱动 report */ }
//!         Task::Recover{after_ms} => { /* 暂时全部断链：睡眠后再 next_task()，到期自动探测 */ }
//!         Task::Done | Task::Failed(_) => break,
//!     }
//! }
//! let state = p.export_state();                // 平台持久化，下次冷启动用
//! ```
//!
//! 设计要点：
//! - 边传边测：分片大小只由文件大小决定（pick_chunk_size_for），describe/init **不再等探测**，
//!   首字节立即发出；探测与传输**并发**——传输期数据没派完就优先并发派探测（上限
//!   MAX_CONCURRENT_PROBES），实测样本回来后把该节点纳入 DRR 池/调窗。这样慢节点/超时
//!   不会阻塞其它节点测量，也不会出现"极快主链路把文件传完、其余节点还没测"的情况。
//! - 启动窗口：主节点（init 锚点，已证明可达）没有新鲜样本时也先给 BOOTSTRAP_WINDOW 并发开传；
//!   新鲜（<10min）的持久化样本直接复用不重复测。
//! - 同后端校验：测速响应里的 node 名必须等于主节点，不一致的节点剔除出上传池
//!   （用户自配节点可能指向了别的后端，多路径必须落在同一会话上）。
//! - 父链折算：子节点有效带宽 = min(自身实测, 父节点有效带宽)，共享瓶颈在此体现。
//! - 调度：DRR 赤字轮询，节点权重 = eff_bps^weight_exponent（默认 0.5=平方根，多链路更均衡；
//!   1.0=严格按带宽，吞吐最优但会饿死慢链路）。
//! - 运行时：每节点独立 AIMD 窗口（上限 MAX_PER_NODE_CONCURRENCY），超时/传输错误
//!   减半，连续成功且传片耗时在链路预算内才加一；422 类数据错误不算拥塞。
//! - 暂时断链：所有节点出池但仍有分片待传时返回 Recover，默认等待 1 秒；
//!   平台睡眠后再次调用 next_task，调度器自动重新探测，不把暂时断链判成永久失败。

use std::collections::HashMap;
use std::time::Duration;

use serde::{Deserialize, Serialize};

mod ffi;
mod scheduler;
#[cfg(test)]
mod tests;

pub use ffi::{
    fc_planner_export, fc_planner_free, fc_planner_new, fc_planner_next, fc_planner_report,
    fc_planner_resume,
};
pub use scheduler::Planner;

/// 持久化状态/接口约定的版本号，结构变更时 +1。
pub const PLANNER_VERSION: u32 = 2;

pub const MAX_PER_NODE_CONCURRENCY: usize = 32;
pub const MIN_CHUNK: u64 = 64 << 10;
pub const MAX_CHUNK: u64 = 8 << 20;
/// 叶子哈希 = 片数×64B 进 init 体与 Redis 会话，片数必须封顶。
pub const MAX_CHUNKS: u32 = 16384;
/// 吞吐测速样本大小（平台可忽略 task.bytes 自行决定，这里给缺省）。
pub const PROBE_THROUGHPUT_BYTES: u64 = 2 << 20;
/// 单片派发总预算（平台不应再对该片做内部重试，调度器就是重试机制）。
pub const CHUNK_ATTEMPTS: u32 = 3;
/// 窗口调整冷却，防振荡。
pub const ADJUST_COOLDOWN_MS: u64 = 2000;
/// 连续成功多少片才允许加窗。
pub const STREAK_TO_INCREASE: u32 = 4;
pub const TIMEOUT_MIN: Duration = Duration::from_secs(15);
pub const TIMEOUT_MAX: Duration = Duration::from_secs(120);
/// 持久化样本的保鲜期。
pub const STATE_FRESH_MS: i64 = 600_000;

pub const CHUNK_TIERS: [u64; 8] = [
    64 << 10,
    128 << 10,
    256 << 10,
    512 << 10,
    1 << 20,
    2 << 20,
    4 << 20,
    8 << 20,
];

/// 单节点在途分片缓冲预算（字节）。每片在上传期间整片驻留内存（客户端 Vec + 服务端
/// 读 body 缓冲），在途上限 ≈ 窗口×分片——不钳制的话极端配置（3 节点×32 路×8MB）
/// 能到 768MB。窗口上限 = max(1, 预算/分片)：8MB 片最多 8 路/节点，64K 片才放开到 32。
pub const MEM_BUDGET_PER_NODE: u64 = 64 << 20;

/// 自适应路径的默认分片（4MiB，与固定路径 DEFAULT_CHUNK_SIZE 对齐）。分片大小不再由带宽
/// 决定：带宽自适应交给运行期 AIMD，这样 describe/init 无需等待探测即可立即开传；
/// 超大文件再按 MAX_CHUNKS 逐档放大。
pub const DEFAULT_UPLOAD_CHUNK: u64 = 4 << 20;

/// 主节点在无实测样本时的启动窗口。init 已证明该节点可达，先给少量并发把首字节发出去，
/// 实测样本回来后由 initial_window 接管，AIMD 再按实况修正（拥塞则减半）。
pub const BOOTSTRAP_WINDOW: usize = 2;

/// 传输期后台探测的并发上限。串行探测会被慢节点/超时拖住（一个 5s 超时就阻塞后面所有
/// 节点的测量），本地回环这种极快链路往往在其它节点测完前就把传输跑完了——于是"只有一两条
/// 链路参与"。放开并发后所有节点能在传输初期同时测完，一起并入 DRR 池。
pub const MAX_CONCURRENT_PROBES: usize = 8;

/// DRR 权重指数的默认值：按带宽平方根加权，多链路更均衡（见 PlannerInput.weight_exponent）。
pub const DEFAULT_WEIGHT_EXPONENT: f64 = 0.5;

// ---------------------------------------------------------------- 输入输出类型

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct PlannerNodeConfig {
    pub id: String,
    pub url: String,
    #[serde(default)]
    pub parent: Option<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct PersistedNodeState {
    pub id: String,
    pub rtt_ms: f64,
    pub bps: f64,
    pub measured_at_ms: i64,
    /// 样本必须绑定入口与后端；旧缓存缺字段时重新实测。
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub backend_node: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct PersistedState {
    #[serde(default)]
    pub nodes: Vec<PersistedNodeState>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct PlannerInput {
    pub total_size: u64,
    /// 主节点 id（通常 = 当前激活节点）。测速从它开始，同后端校验以它为准。
    pub main_node: String,
    pub now_ms: i64,
    pub nodes: Vec<PlannerNodeConfig>,
    #[serde(default)]
    pub state: PersistedState,
    /// 窗口调整冷却（测试可设为 0）。
    #[serde(default = "default_adjust_cooldown")]
    pub adjust_cooldown_ms: u64,
    /// DRR 权重指数：节点权重 = eff_bps^exponent，clamp 到 [0,1]。
    /// 1.0 = 严格按带宽（吞吐最优，极快链路吃满，慢链路基本饿死）；
    /// 0.5 = 按带宽平方根（多链路更均衡，推荐默认）；
    /// 0.0 = 均分（最公平，牺牲总吞吐）。
    #[serde(default = "default_weight_exponent")]
    pub weight_exponent: f64,
}

fn default_adjust_cooldown() -> u64 {
    ADJUST_COOLDOWN_MS
}

fn default_weight_exponent() -> f64 {
    DEFAULT_WEIGHT_EXPONENT
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ProbeKind {
    /// 微样本（bytes=0 或极小体），测 RTT。
    Tiny,
    /// 2MiB 样本，测吞吐。
    Throughput,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ProbeTask {
    pub seq: u64,
    pub node: String,
    pub url: String,
    pub kind: ProbeKind,
    pub bytes: u64,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ChunkTask {
    pub seq: u64,
    pub node: String,
    pub url: String,
    pub index: u32,
    pub timeout_ms: u64,
    pub bytes: u64,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Task {
    Probe(ProbeTask),
    Chunk(ChunkTask),
    /// 会话过期（404）：平台重新 init 后调 resume()。
    Reinit,
    /// 无任务可分派（全部窗口占满）：等在途分片的结果驱动。
    Wait,
    /// 所有节点暂时不可用：宿主等待 after_ms 后再次调用 next_task()。
    Recover {
        after_ms: u64,
    },
    Done,
    Failed {
        reason: String,
    },
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Report {
    ProbeOk {
        node: String,
        kind: ProbeKind,
        bytes: u64,
        elapsed_ms: u64,
        /// 测速响应里的后端节点名（同后端校验依据）。
        backend_node: String,
    },
    ProbeFailed {
        node: String,
        kind: ProbeKind,
    },
    ChunkOk {
        seq: u64,
        bytes: u64,
        elapsed_ms: u64,
    },
    /// 超时/传输层错误：拥塞信号。
    ChunkCongested {
        seq: u64,
    },
    /// 节点断链或超过平台的分片期限：移出本次传输，并重排其全部在途分片。
    ChunkUnavailable {
        seq: u64,
    },
    /// 422 类校验错误：数据问题，不是拥塞。
    ChunkDataError {
        seq: u64,
    },
    /// 404：会话没了，需要平台重新 init 后 resume。
    ChunkSessionGone {
        seq: u64,
    },
}

// ---------------------------------------------------------------- 纯函数（可测）

/// 分片大小只由文件大小决定：默认 DEFAULT_UPLOAD_CHUNK，片数超 MAX_CHUNKS 时逐档放大，
/// 档位用完（>128GiB）就放行超 cap。不再依赖带宽——带宽自适应交给运行期 AIMD，
/// 这样 describe/init 无需等待探测，可立即开传（见模块注释「边传边测」）。
pub fn pick_chunk_size_for(total_size: u64) -> u64 {
    let mut pick = DEFAULT_UPLOAD_CHUNK.clamp(MIN_CHUNK, MAX_CHUNK);
    while total_size.div_ceil(pick) > MAX_CHUNKS as u64 {
        match CHUNK_TIERS.iter().find(|&&t| t > pick) {
            Some(&next) => pick = next,
            None => break,
        }
    }
    pick
}

/// 每流目标吞吐：一条健康 HTTPS 流到 VPS 中继的典型可承载量（经验值）。
/// 初始窗口 = ceil(有效带宽 / 该值)。
///
/// 为什么不用教科书 BDP=带宽×RTT：分片档位与带宽解耦后（见 pick_chunk_size_for），
/// BDP/分片 只由 RTT 决定——任何正常 RTT 下都算出 1 路，32 路上限形同虚设。管道真实
/// 填充交给运行期 AIMD（它按实际成功/失败修正），这里只要给一个有区分度的起点。
pub const PER_STREAM_BPS: f64 = 8_000_000.0;

pub fn initial_window(bps: f64, _rtt: Duration, _chunk: u64) -> usize {
    if bps <= 0.0 {
        return 1;
    }
    ((bps / PER_STREAM_BPS).ceil() as usize).clamp(1, MAX_PER_NODE_CONCURRENCY)
}

/// 单片动态超时 = RTT + 2×(分片/带宽EMA)，clamp [15s, 120s]。
/// 分片大小不再按带宽调档后，首次上传某节点时还没有吞吐 EMA：给足上限时间（TIMEOUT_MAX）
/// 让首片自然完成，免得慢链路上的第一片被误判拥塞；EMA 建立后按 2×实际传输时间收紧。
pub fn chunk_timeout(rtt: Duration, bps_ema: Option<f64>, chunk: u64) -> Duration {
    let transfer = match bps_ema {
        Some(b) if b > 0.0 => Duration::from_secs_f64(2.0 * chunk as f64 / b),
        _ => TIMEOUT_MAX,
    };
    (rtt + transfer).clamp(TIMEOUT_MIN, TIMEOUT_MAX)
}

/// 父链折算：子节点有效带宽 = min(自身, 父节点有效)。父缺失/不健康则不加约束。
/// 返回 id → 有效带宽；measured 里没有的节点视为无有效带宽。
pub fn combine_effective(
    nodes: &[PlannerNodeConfig],
    measured: &HashMap<String, f64>,
) -> HashMap<String, f64> {
    let mut eff: HashMap<String, f64> = HashMap::new();
    // 迭代加深：父链最长按节点数收敛
    for _ in 0..nodes.len().max(1) {
        let mut changed = false;
        for n in nodes {
            if eff.contains_key(&n.id) {
                continue;
            }
            let self_m = match measured.get(&n.id) {
                Some(&m) if m > 0.0 => m,
                _ => continue,
            };
            let v = match &n.parent {
                Some(p) => match eff.get(p) {
                    Some(&pe) => self_m.min(pe),
                    // 父还没算出来：下一轮再算（除非父根本测不出，那一轮轮空后此分支持续 None→按无约束算）
                    None => match measured.get(p) {
                        Some(_) => {
                            continue;
                        }
                        None => self_m,
                    },
                },
                None => self_m,
            };
            eff.insert(n.id.clone(), v);
            changed = true;
        }
        if !changed {
            break;
        }
    }
    eff
}

fn probe_bytes(kind: ProbeKind) -> u64 {
    match kind {
        ProbeKind::Tiny => 0,
        ProbeKind::Throughput => PROBE_THROUGHPUT_BYTES,
    }
}

/// 测序：主节点最先，其余父先于子（拓扑排序，环退化为配置序，缺的 parent 忽略）。
fn probe_order(nodes: &[PlannerNodeConfig], main: &str) -> Vec<String> {
    let mut order: Vec<String> = Vec::with_capacity(nodes.len());
    if nodes.iter().any(|n| n.id == main) {
        order.push(main.to_string());
    }
    let mut remaining: Vec<&PlannerNodeConfig> = nodes.iter().filter(|n| n.id != main).collect();
    // 简单拓扑：最多 n 轮，每轮放所有「父已在 order 或无父」的节点
    for _ in 0..nodes.len() {
        let mut progressed = false;
        remaining.retain(|n| {
            let parent_ready = match &n.parent {
                Some(p) => order.iter().any(|id| id == p),
                None => true,
            };
            if parent_ready {
                order.push(n.id.clone());
                progressed = true;
                false
            } else {
                true
            }
        });
        if remaining.is_empty() || !progressed {
            break;
        }
    }
    for n in remaining {
        order.push(n.id.clone());
    }
    order
}
