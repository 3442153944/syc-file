//! upload_planner.rs
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

use std::collections::{BTreeMap, HashMap, HashSet, VecDeque};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};

/// 持久化状态/接口约定的版本号，结构变更时 +1。
pub const PLANNER_VERSION: u32 = 1;

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

// ---------------------------------------------------------------- 调度器

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Phase {
    /// 尚未 begin_transfer：平台可先取探测任务，队列派完返回 Wait。
    Idle,
    Transfer,
    NeedReinit,
    Finished,
}

#[derive(Debug)]
struct NodeRuntime {
    cfg: PlannerNodeConfig,
    /// 本次会话实测（探针）或新鲜持久化样本。
    rtt: Option<Duration>,
    bps: Option<f64>,
    /// 主节点测速响应里的后端节点名（同后端校验用）。
    backend_node: Option<String>,
    measured_at_ms: Option<i64>,
    /// 探测失败（本次探测且没有新鲜持久化兜底）。
    probe_dead: bool,
    transfer_dead: bool,
    window: usize,
    /// 内存预算钳制的窗口上限（按分片大小算好），AIMD 加窗也不得越过。
    window_cap: usize,
    deficit: f64,
    streak: u32,
    last_adjust: Option<Instant>,
    /// 传输期 EMA（bytes/s），由成功分片持续更新。
    ema_bps: Option<f64>,
    /// 父链折算后的有效带宽（DRR 权重/窗口依据）；0 = 出池（不可用）。
    eff_bps: f64,
}

impl NodeRuntime {
    fn effective_bps(&self) -> f64 {
        self.eff_bps
    }
}

struct Inflight {
    node: String,
    index: u32,
}

pub struct Planner {
    total_size: u64,
    chunk_size: u64,
    adjust_cooldown: Duration,
    now_ms: i64,
    main_node: String,
    /// DRR 权重指数（见 PlannerInput.weight_exponent），已 clamp [0,1]。
    weight_exponent: f64,
    phase: Phase,
    nodes: Vec<NodeRuntime>,
    /// 探测队列：node id + kind，按测序顺序。
    probe_queue: VecDeque<(String, ProbeKind)>,
    /// 在途探测集合（node, kind）：并发派发时按 (node,kind) 匹配回报，过期/乱序的忽略。
    probing: HashSet<(String, ProbeKind)>,
    seq: u64,
    upload_id: String,
    /// index -> 剩余派发预算。
    pending: BTreeMap<u32, u32>,
    dispatch_queue: VecDeque<u32>,
    inflight: HashMap<u64, Inflight>,
    reinit_notified: bool,
    fail_reason: Option<String>,
}

impl Planner {
    pub fn new(input: PlannerInput) -> Result<Self, String> {
        if input.nodes.is_empty() {
            return Err("节点池为空".into());
        }
        let ids: HashSet<_> = input.nodes.iter().map(|n| n.id.as_str()).collect();
        if ids.len() != input.nodes.len() || !ids.contains(input.main_node.as_str()) {
            return Err("节点 ID 重复或主节点不在节点池中".into());
        }
        let main = input.main_node.clone();
        let cooldown = Duration::from_millis(input.adjust_cooldown_ms);
        let fresh: HashMap<String, &PersistedNodeState> = input
            .state
            .nodes
            .iter()
            .filter(|n| {
                let age = input.now_ms.checked_sub(n.measured_at_ms);
                matches!(age, Some(0..=STATE_FRESH_MS))
                    && n.rtt_ms.is_finite()
                    && n.rtt_ms > 0.0
                    && n.rtt_ms <= 120_000.0
                    && n.bps.is_finite()
                    && n.bps > 0.0
                    && !n.backend_node.is_empty()
                    && input
                        .nodes
                        .iter()
                        .any(|cfg| cfg.id == n.id && cfg.url == n.url)
            })
            .map(|n| (n.id.clone(), n))
            .collect();

        // 测序：主节点最先；其余按「父节点先于子节点」拓扑排序（带环保护，环上退化为配置序）
        let order = probe_order(&input.nodes, &main);

        let mut nodes: Vec<NodeRuntime> = Vec::with_capacity(input.nodes.len());
        let mut probe_queue: VecDeque<(String, ProbeKind)> = VecDeque::new();
        for id in &order {
            let cfg = input
                .nodes
                .iter()
                .find(|n| &n.id == id)
                .cloned()
                .ok_or_else(|| format!("节点 {} 配置缺失", id))?;
            let (rtt, bps) = match fresh.get(id) {
                Some(s) => (
                    Some(Duration::from_secs_f64(s.rtt_ms / 1000.0)),
                    Some(s.bps),
                ),
                None => (None, None),
            };
            if rtt.is_none() {
                // 无新鲜样本：排两个探针任务（微样本 + 吞吐），传输期并发补测
                probe_queue.push_back((id.clone(), ProbeKind::Tiny));
                probe_queue.push_back((id.clone(), ProbeKind::Throughput));
            }
            nodes.push(NodeRuntime {
                cfg,
                rtt,
                bps,
                backend_node: fresh.get(id).map(|s| s.backend_node.clone()),
                measured_at_ms: fresh.get(id).map(|s| s.measured_at_ms),
                probe_dead: false,
                transfer_dead: false,
                window: 0,
                window_cap: MAX_PER_NODE_CONCURRENCY,
                deficit: 0.0,
                streak: 0,
                last_adjust: None,
                ema_bps: None,
                eff_bps: 0.0,
            });
        }

        Ok(Planner {
            total_size: input.total_size,
            chunk_size: 0,
            adjust_cooldown: cooldown,
            now_ms: input.now_ms,
            main_node: main,
            weight_exponent: input.weight_exponent.clamp(0.0, 1.0),
            phase: Phase::Idle,
            nodes,
            probe_queue,
            probing: HashSet::new(),
            seq: 0,
            upload_id: String::new(),
            pending: BTreeMap::new(),
            dispatch_queue: VecDeque::new(),
            inflight: HashMap::new(),
            reinit_notified: false,
            fail_reason: None,
        })
    }

    /// 选片结果：分片大小只由文件大小决定，`Planner::new` 之后即可用（无需等探测）。
    pub fn planned_chunk_size(&mut self) -> Option<u64> {
        self.ensure_sized();
        (self.chunk_size != 0).then_some(self.chunk_size)
    }

    /// Preserve the chunk contract when resuming an existing server session.
    pub fn set_session_chunk_size(&mut self, size: u64) -> Result<(), String> {
        if !matches!(self.phase, Phase::Idle) || size == 0 || size > (1 << 30) {
            return Err("invalid session chunk size or transfer already started".into());
        }
        self.chunk_size = size;
        Ok(())
    }

    /// 每个节点的初始窗口（调试用/日志用）。
    pub fn windows(&self) -> Vec<(String, usize)> {
        self.nodes
            .iter()
            .map(|n| (n.cfg.id.clone(), n.window))
            .collect()
    }

    /// 平台 describe/init 完成，开始传分片。此时不要求探测已完成——主节点按启动窗口先开传，
    /// 探测结果稍后经 report() 回来再并入调度。
    pub fn begin_transfer(&mut self, upload_id: &str, missing: &[u32]) {
        self.ensure_sized();
        self.recompute_windows();
        self.upload_id = upload_id.to_string();
        self.pending = missing.iter().map(|&i| (i, CHUNK_ATTEMPTS)).collect();
        self.dispatch_queue = missing.iter().copied().collect();
        self.inflight.clear();
        self.phase = Phase::Transfer;
        self.reinit_notified = false;
    }

    /// 会话过期后重新接入（平台也可用它做首个会话接入，与 begin_transfer 同效：
    /// 会在需要时按当前样本/bootstrap 重算窗口）。
    pub fn resume(&mut self, upload_id: &str, missing: &[u32]) {
        // 平台已取消旧会话在途任务，被取消的探针需要重新排队。
        for (node, kind) in self.probing.drain() {
            self.probe_queue.push_back((node, kind));
        }
        self.ensure_sized();
        self.recompute_windows();
        self.upload_id = upload_id.to_string();
        self.pending = missing.iter().map(|&i| (i, CHUNK_ATTEMPTS)).collect();
        self.dispatch_queue = missing.iter().copied().collect();
        self.inflight.clear();
        self.phase = Phase::Transfer;
        self.reinit_notified = false;
    }

    pub fn next_task(&mut self) -> Task {
        match self.phase {
            Phase::Finished => {
                return match &self.fail_reason {
                    Some(r) => Task::Failed { reason: r.clone() },
                    None => Task::Done,
                };
            }
            Phase::NeedReinit => {
                if !self.reinit_notified {
                    self.reinit_notified = true;
                    return Task::Reinit;
                }
                // 已通知过、等平台 resume：其间仍可把剩余探测派出去
                if let Some(t) = self.take_probe_task() {
                    return Task::Probe(t);
                }
                return Task::Wait;
            }
            _ => {}
        }
        self.ensure_sized();

        // 传输期：数据没传完就先派探测（一次一个），探测在途时空出来的窗口交给分片——
        // 这样多线路测速在传输一开始就并发跑起来（不阻塞首字节，也不被分派饿死），
        // 探测结果回来即并入 DRR 池。数据一旦派完就收尾，不再补派探测。
        if self.phase == Phase::Transfer {
            if self.pending.is_empty() && self.inflight.is_empty() {
                self.phase = Phase::Finished;
                return Task::Done;
            }
            if let Some(t) = self.take_probe_task() {
                return Task::Probe(t);
            }
            if let Some(t) = self.take_chunk_task() {
                return Task::Chunk(t);
            }
            if self.inflight.is_empty()
                && self.probing.is_empty()
                && self.nodes.iter().all(|n| n.window == 0)
            {
                self.phase = Phase::Finished;
                self.fail_reason = Some("所有上传节点均不可用，未确认分片无法重传".into());
                return Task::Failed {
                    reason: self.fail_reason.clone().unwrap(),
                };
            }
            return Task::Wait;
        }
        if let Some(t) = self.take_probe_task() {
            return Task::Probe(t);
        }
        Task::Wait
    }

    /// 平台循环的调试/日志辅助：当前所处阶段名。
    pub fn phase_name(&self) -> &'static str {
        match self.phase {
            Phase::Idle => "idle",
            Phase::Transfer => "transfer",
            Phase::NeedReinit => "need_reinit",
            Phase::Finished => "finished",
        }
    }

    pub fn report(&mut self, r: Report) {
        match r {
            Report::ProbeOk {
                node,
                kind,
                bytes,
                elapsed_ms,
                backend_node,
            } => self.on_probe_ok(&node, kind, bytes, elapsed_ms, &backend_node),
            Report::ProbeFailed { node, kind } => self.on_probe_failed(&node, kind),
            Report::ChunkOk {
                seq,
                bytes,
                elapsed_ms,
            } => self.on_chunk_done(seq, true, bytes, elapsed_ms, false),
            Report::ChunkCongested { seq } => self.on_chunk_done(seq, false, 0, 0, true),
            Report::ChunkUnavailable { seq } => self.on_node_unavailable(seq),
            Report::ChunkDataError { seq } => self.on_chunk_done(seq, false, 0, 0, false),
            Report::ChunkSessionGone { seq } => self.on_session_gone(seq),
        }
    }

    pub fn export_state(&self) -> PersistedState {
        PersistedState {
            nodes: self
                .nodes
                .iter()
                .filter_map(|n| {
                    if n.probe_dead || n.transfer_dead {
                        return None;
                    }
                    let rtt = n.rtt?;
                    let bps = n.bps?;
                    Some(PersistedNodeState {
                        id: n.cfg.id.clone(),
                        rtt_ms: rtt.as_secs_f64() * 1000.0,
                        bps,
                        measured_at_ms: n.measured_at_ms?,
                        url: n.cfg.url.clone(),
                        backend_node: n.backend_node.clone()?,
                    })
                })
                .collect(),
        }
    }

    // ---- 探测 ----

    fn take_probe_task(&mut self) -> Option<ProbeTask> {
        // 并发派发但有上限：慢节点/超时不再阻塞其它节点的测量（见 MAX_CONCURRENT_PROBES）
        if self.probing.len() >= MAX_CONCURRENT_PROBES {
            return None;
        }
        match self.probe_queue.pop_front() {
            Some((node, kind)) => {
                self.seq += 1;
                self.probing.insert((node.clone(), kind));
                Some(ProbeTask {
                    seq: self.seq,
                    node: node.clone(),
                    url: self.node_url(&node),
                    kind,
                    bytes: probe_bytes(kind),
                })
            }
            None => None,
        }
    }

    fn on_probe_ok(
        &mut self,
        node: &str,
        kind: ProbeKind,
        bytes: u64,
        elapsed_ms: u64,
        backend_node: &str,
    ) {
        if !self.probing.remove(&(node.to_string(), kind)) {
            return; // 乱序/过期探测结果，忽略
        }
        let measured_at = self.now_ms;
        {
            let n = match self.node_mut(node) {
                Some(n) => n,
                None => return,
            };
            n.backend_node = Some(backend_node.to_string());
            if backend_node.is_empty() {
                n.probe_dead = true;
            } else {
                let elapsed = Duration::from_millis(elapsed_ms.max(1));
                match kind {
                    ProbeKind::Tiny => {
                        n.rtt = Some(elapsed);
                    }
                    ProbeKind::Throughput => {
                        if bytes > 0 {
                            n.bps = Some(bytes as f64 / elapsed.as_secs_f64());
                            n.measured_at_ms = Some(measured_at);
                        }
                    }
                }
            }
        }
        // 样本变化即时并入调度：传输期新测出的节点借此进入 DRR 池、窗口按实测调整
        self.recompute_windows();
    }

    fn on_probe_failed(&mut self, node: &str, kind: ProbeKind) {
        if !self.probing.remove(&(node.to_string(), kind)) {
            return;
        }
        // tiny 失败说明节点不可达：后续探测也没意义，清空队列里它的任务
        if kind == ProbeKind::Tiny {
            self.probe_queue.retain(|(id, _)| id != node);
            if let Some(n) = self.node_mut(node) {
                n.probe_dead = true;
            }
        }
        self.recompute_windows();
    }

    /// 分片大小只由文件大小决定，`new` 之后即可算（幂等）。
    fn ensure_sized(&mut self) {
        if self.chunk_size == 0 {
            self.chunk_size = pick_chunk_size_for(self.total_size);
        }
    }

    /// 依据当前已知样本重算每节点的有效带宽/窗口/窗口上限，并做出池判定。
    /// `begin_transfer` 调一次（此刻可能只有持久化样本甚至没有），此后每次探测回报再调，
    /// 让后台补测出的线路逐步并入。已有窗口只增不减（保留 AIMD 已经涨上去的成果），
    /// 除非该节点被判出池（同后端不符/探测死亡）。
    fn recompute_windows(&mut self) {
        self.ensure_sized();
        let chunk = self.chunk_size;
        let window_cap =
            ((MEM_BUDGET_PER_NODE / chunk).max(1) as usize).clamp(1, MAX_PER_NODE_CONCURRENCY);
        let main_id = self.main_id();
        let main_backend = self
            .nodes
            .iter()
            .find(|n| n.cfg.id == main_id)
            .and_then(|n| n.backend_node.clone())
            .unwrap_or_default();

        // 出池判定：探测死亡（且无新鲜样本）或同后端校验不符
        for n in &mut self.nodes {
            n.window_cap = window_cap;
            n.eff_bps = 0.0;
            let dead = n.probe_dead || n.transfer_dead;
            let mismatch = n
                .backend_node
                .as_deref()
                .map(|b| b.is_empty() || (!main_backend.is_empty() && b != main_backend))
                .unwrap_or(false);
            // 主节点身份尚未实测时，仅让 init 锚点开传，不把其它入口加入会话。
            let unverified =
                n.cfg.id != main_id && (main_backend.is_empty() || n.backend_node.is_none());
            if dead || mismatch || unverified {
                if dead || mismatch {
                    n.bps = None;
                }
                n.window = 0;
            }
        }

        let cfgs: Vec<PlannerNodeConfig> = self.nodes.iter().map(|n| n.cfg.clone()).collect();
        let measured: HashMap<String, f64> = self
            .nodes
            .iter()
            .filter(|n| {
                n.rtt.is_some()
                    && !n.probe_dead
                    && !n.transfer_dead
                    && !main_backend.is_empty()
                    && n.backend_node.as_deref() == Some(main_backend.as_str())
            })
            .map(|n| (n.cfg.id.clone(), n.bps.unwrap_or(0.0)))
            .collect();
        let eff = combine_effective(&cfgs, &measured);

        for n in &mut self.nodes {
            if let (Some(&e), Some(rtt)) = (eff.get(&n.cfg.id), n.rtt) {
                if e > 0.0 {
                    n.eff_bps = e;
                    let w = initial_window(e, rtt, chunk).min(window_cap);
                    if w > n.window {
                        n.window = w;
                    }
                }
            }
        }

        // 主节点是 init 锚点（已证明可达）：没有实测先验时也给启动窗口。
        // init 锚点始终保留 bootstrap，不能因为副节点先测完就饿死主链路。
        {
            if let Some(n) = self.nodes.iter_mut().find(|n| n.cfg.id == main_id) {
                if n.window == 0 && !n.transfer_dead {
                    n.window = BOOTSTRAP_WINDOW.min(window_cap);
                }
            }
        }
    }

    // ---- 传输 ----

    fn take_chunk_task(&mut self) -> Option<ChunkTask> {
        // 从派发队列取还有预算的片
        let index = loop {
            match self.dispatch_queue.pop_front() {
                None => return None,
                Some(i) => {
                    if self.pending.contains_key(&i) {
                        break i;
                    }
                }
            }
        };
        // DRR（赤字轮询）：deficit 最大且有空窗的节点胜出；
        // 分派后 deficit += 自身权重 - 总权重（允许为负），权重占比大的节点自然多拿。
        // 权重 = eff_bps^exponent：exponent=1 严格按带宽；0.5 平方根（默认，多链路更均衡）；
        // 0 均分。慢节点不会因快节点极快而被完全饿死。
        let exp = self.weight_exponent;
        let weight = |n: &NodeRuntime| n.effective_bps().max(1.0).powf(exp);
        let total_weight: f64 = self.nodes.iter().filter(|n| n.window > 0).map(weight).sum();
        let mut best: Option<usize> = None;
        let mut best_deficit = f64::NEG_INFINITY;
        for (i, n) in self.nodes.iter().enumerate() {
            if n.window == 0 {
                continue;
            }
            let inflight_n = self
                .inflight
                .values()
                .filter(|v| v.node == n.cfg.id)
                .count();
            if inflight_n >= n.window {
                continue;
            }
            if n.deficit > best_deficit {
                best_deficit = n.deficit;
                best = Some(i);
            }
        }
        let ni = match best {
            Some(i) => i,
            None => {
                self.dispatch_queue.push_front(index);
                return None; // 全部窗口占满/无可用节点
            }
        };
        self.seq += 1;
        let seq = self.seq;
        {
            let n = &mut self.nodes[ni];
            n.deficit += weight(n) - total_weight;
        }
        let node = &self.nodes[ni];
        let timeout = chunk_timeout(
            node.rtt.unwrap_or(Duration::from_secs(1)),
            node.ema_bps,
            self.chunk_size,
        );
        self.inflight.insert(
            seq,
            Inflight {
                node: node.cfg.id.clone(),
                index,
            },
        );
        Some(ChunkTask {
            seq,
            node: node.cfg.id.clone(),
            url: node.cfg.url.clone(),
            index,
            timeout_ms: timeout.as_millis() as u64,
            bytes: self.chunk_size,
        })
    }

    /// 平台据此取消已被重排的请求，并忽略其迟到回报（包括进度计数）。
    pub fn is_chunk_inflight(&self, seq: u64) -> bool {
        self.inflight.contains_key(&seq)
    }

    fn on_node_unavailable(&mut self, seq: u64) {
        let node = match self.inflight.get(&seq) {
            Some(v) => v.node.clone(),
            None => return,
        };
        if let Some(n) = self.node_mut(&node) {
            n.transfer_dead = true;
            n.window = 0;
            n.eff_bps = 0.0;
            n.streak = 0;
        }
        self.probe_queue.retain(|(id, _)| id != &node);
        self.probing.retain(|(id, _)| id != &node);
        // 取消的是链路上的尝试，未确认的文件分片仍必须在健康节点上传。
        let mut retry = Vec::new();
        self.inflight.retain(|_, v| {
            if v.node == node {
                retry.push(v.index);
                false
            } else {
                true
            }
        });
        retry.sort_unstable();
        for index in retry.into_iter().rev() {
            if self.pending.contains_key(&index) {
                self.dispatch_queue.push_front(index);
            }
        }
        self.recompute_windows();
    }

    fn on_chunk_done(&mut self, seq: u64, ok: bool, bytes: u64, elapsed_ms: u64, congested: bool) {
        let (node_id, index) = match self.inflight.remove(&seq) {
            Some(v) => (v.node, v.index),
            None => return,
        };
        let attempts_left = match self.pending.get(&index) {
            Some(&a) => a,
            None => return,
        };
        let cooldown = self.adjust_cooldown;
        if ok {
            self.pending.remove(&index);
            if let Some(n) = self.node_mut(&node_id) {
                n.streak += 1;
                let bps = bytes as f64 / Duration::from_millis(elapsed_ms.max(1)).as_secs_f64();
                n.ema_bps = Some(match n.ema_bps {
                    Some(e) => 0.7 * e + 0.3 * bps,
                    None => bps,
                });
            }
            self.maybe_increase_window(&node_id);
            return;
        }
        // 失败：拥塞类信号减半窗口（带冷却），数据类只消耗预算
        if let Some(n) = self.node_mut(&node_id) {
            n.streak = 0;
            let cooldown_ok = n
                .last_adjust
                .map(|t| t.elapsed() >= cooldown)
                .unwrap_or(true);
            if congested && cooldown_ok {
                n.window = (n.window / 2).max(1);
                n.last_adjust = Some(Instant::now());
            }
        }
        if attempts_left <= 1 {
            self.pending.remove(&index);
            self.phase = Phase::Finished;
            self.fail_reason = Some(format!(
                "分片 {} 重试耗尽（最后错误：{}）",
                index,
                if congested { "拥塞" } else { "数据校验" }
            ));
        } else {
            self.pending.insert(index, attempts_left - 1);
            self.dispatch_queue.push_front(index);
        }
    }

    fn on_session_gone(&mut self, seq: u64) {
        if self.inflight.remove(&seq).is_none() {
            return;
        }
        if self.phase == Phase::Transfer {
            self.phase = Phase::NeedReinit;
            self.reinit_notified = false;
        }
    }

    fn maybe_increase_window(&mut self, node_id: &str) {
        let cooldown = self.adjust_cooldown;
        let chunk = self.chunk_size;
        let (streak, window, window_cap, last_adjust, baseline, probe_bps, ema_bps) = {
            let n = match self.node_mut(node_id) {
                Some(n) => n,
                None => return,
            };
            (
                n.streak,
                n.window,
                n.window_cap,
                n.last_adjust,
                n.rtt.unwrap_or(Duration::from_secs(1)),
                n.bps,
                n.ema_bps,
            )
        };
        if streak < STREAK_TO_INCREASE || window == 0 || window >= window_cap {
            return;
        }
        let cooldown_ok = last_adjust.map(|t| t.elapsed() >= cooldown).unwrap_or(true);
        if !cooldown_ok {
            return;
        }
        // 分片耗时包括发送字节，不能只和 RTT 比：WAN 上 4MiB / 带宽通常远大于 RTT，
        // 旧判定会让健康低带宽线路永远无法加窗。用探测带宽估算完整传片预算。
        let avg = match ema_bps {
            Some(b) if b > 0.0 => Duration::from_secs_f64(chunk as f64 / b),
            _ => Duration::ZERO,
        };
        let budget = probe_bps
            .filter(|b| *b > 0.0)
            .map(|b| baseline + Duration::from_secs_f64(chunk as f64 / b));
        if budget.map(|b| avg <= b.mul_f64(2.0)).unwrap_or(true) {
            if let Some(n) = self.node_mut(node_id) {
                n.window += 1;
                n.streak = 0;
                n.last_adjust = Some(Instant::now());
            }
        }
    }

    // ---- 工具 ----

    fn main_id(&self) -> String {
        self.main_node.clone()
    }

    fn node_url(&self, id: &str) -> String {
        self.nodes
            .iter()
            .find(|n| n.cfg.id == id)
            .map(|n| n.cfg.url.clone())
            .unwrap_or_default()
    }

    fn node_mut(&mut self, id: &str) -> Option<&mut NodeRuntime> {
        self.nodes.iter_mut().find(|n| n.cfg.id == id)
    }

    /// 终态原因（Failed 时）。
    pub fn fail_reason(&self) -> Option<&str> {
        self.fail_reason.as_deref()
    }
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

// ---------------------------------------------------------------- C ABI（JNI/cgo 同构使用）
//
// 边界两侧一律走 JSON 字符串（与 fc_sys_snapshot 同一风格），句柄不透明。
// 平台循环：fc_planner_next 拿 JSON 任务 → 执行 → fc_planner_report 回传 JSON 结果。

use std::ffi::CString;
use std::os::raw::{c_char, c_void};
use std::panic::{catch_unwind, AssertUnwindSafe};

fn cstr(p: *const c_char) -> Result<String, i32> {
    if p.is_null() {
        return Err(-10);
    }
    let s = unsafe { std::ffi::CStr::from_ptr(p) };
    s.to_str().map(|x| x.to_string()).map_err(|_| -11)
}

fn json_string(v: &impl Serialize) -> *mut c_char {
    match serde_json::to_string(v) {
        Ok(s) => CString::new(s)
            .map(|c| c.into_raw())
            .unwrap_or(std::ptr::null_mut()),
        Err(_) => std::ptr::null_mut(),
    }
}

fn planner_ref(h: *mut c_void) -> Result<&'static mut Planner, i32> {
    if h.is_null() {
        return Err(-1);
    }
    Ok(unsafe { &mut *(h as *mut Planner) })
}

/// 创建 Planner。desc_json = PlannerInput 序列化。失败返回 null。
#[no_mangle]
pub extern "C" fn fc_planner_new(desc_json: *const c_char) -> *mut c_void {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let s = match cstr(desc_json) {
            Ok(s) => s,
            Err(_) => return std::ptr::null_mut(),
        };
        match serde_json::from_str::<PlannerInput>(&s) {
            Ok(input) => match Planner::new(input) {
                Ok(p) => Box::into_raw(Box::new(p)) as *mut c_void,
                Err(_) => std::ptr::null_mut(),
            },
            Err(_) => std::ptr::null_mut(),
        }
    }));
    r.unwrap_or(std::ptr::null_mut())
}

/// 取下一个任务（JSON）。终态返回 {"type":"done"} / {"type":"failed","reason":..}。
#[no_mangle]
pub extern "C" fn fc_planner_next(handle: *mut c_void) -> *mut c_char {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let p = match planner_ref(handle) {
            Ok(p) => p,
            Err(_) => {
                return json_string(&Task::Failed {
                    reason: "无效句柄".into(),
                })
            }
        };
        json_string(&p.next_task())
    }));
    r.unwrap_or_else(|_| {
        json_string(&Task::Failed {
            reason: "panic".into(),
        })
    })
}

/// 回传结果（JSON 的 Report）。返回 0 成功，负数失败。
#[no_mangle]
pub extern "C" fn fc_planner_report(handle: *mut c_void, report_json: *const c_char) -> i32 {
    let r = catch_unwind(AssertUnwindSafe(|| -> Result<i32, i32> {
        let p = planner_ref(handle)?;
        let s = cstr(report_json)?;
        let rep: Report = serde_json::from_str(&s).map_err(|_| -12)?;
        p.report(rep);
        Ok(0)
    }));
    r.unwrap_or(Err(-1)).unwrap_or(-1)
}

/// 会话过期重新接入。missing_json = [u32..]。返回 0 成功。
#[no_mangle]
pub extern "C" fn fc_planner_resume(
    handle: *mut c_void,
    upload_id: *const c_char,
    missing_json: *const c_char,
) -> i32 {
    let r = catch_unwind(AssertUnwindSafe(|| -> Result<i32, i32> {
        let p = planner_ref(handle)?;
        let id = cstr(upload_id)?;
        let s = cstr(missing_json)?;
        let missing: Vec<u32> = serde_json::from_str(&s).map_err(|_| -12)?;
        p.resume(&id, &missing);
        Ok(0)
    }));
    r.unwrap_or(Err(-1)).unwrap_or(-1)
}

/// 导出持久化状态（JSON 的 PersistedState）。平台负责落盘。
#[no_mangle]
pub extern "C" fn fc_planner_export(handle: *mut c_void) -> *mut c_char {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let p = match planner_ref(handle) {
            Ok(p) => p,
            Err(_) => return std::ptr::null_mut(),
        };
        json_string(&p.export_state())
    }));
    r.unwrap_or(std::ptr::null_mut())
}

#[no_mangle]
pub extern "C" fn fc_planner_free(handle: *mut c_void) {
    if !handle.is_null() {
        let _ = catch_unwind(AssertUnwindSafe(|| unsafe {
            drop(Box::from_raw(handle as *mut Planner));
        }));
    }
}

// ---------------------------------------------------------------- 测试

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unavailable_node_requeues_all_chunks_and_ignores_late_reports() {
        let p = Planner::new(input(
            vec![node("main", None), node("hk", Some("main"))],
            "main",
            1_000_000,
        ))
        .unwrap();
        let mut p = run_probes(p, &[]);
        p.begin_transfer("u1", &[0, 1, 2]);
        let mut sent = Vec::new();
        while let Task::Chunk(t) = p.next_task() {
            sent.push(t);
        }
        let dead = &sent[0].node;
        let retired: Vec<_> = sent.iter().filter(|t| &t.node == dead).collect();
        assert!(retired.len() >= 2, "覆盖同节点多个在途分片");
        p.report(Report::ChunkUnavailable {
            seq: retired[0].seq,
        });
        assert_eq!(
            p.windows()
                .into_iter()
                .find(|(id, _)| id == dead)
                .unwrap()
                .1,
            0
        );
        for t in &retired {
            assert!(!p.is_chunk_inflight(t.seq));
            p.report(Report::ChunkOk {
                seq: t.seq,
                bytes: t.bytes,
                elapsed_ms: 10,
            });
            p.report(Report::ChunkSessionGone { seq: t.seq });
        }
        assert_eq!(p.phase_name(), "transfer");
        assert!(p.export_state().nodes.iter().all(|n| &n.id != dead));
        for t in sent.iter().filter(|t| &t.node != dead) {
            p.report(Report::ChunkOk {
                seq: t.seq,
                bytes: t.bytes,
                elapsed_ms: 100,
            });
        }
        let mut retried = Vec::new();
        loop {
            match p.next_task() {
                Task::Chunk(t) => {
                    assert_ne!(&t.node, dead);
                    retried.push(t.index);
                    p.report(Report::ChunkOk {
                        seq: t.seq,
                        bytes: t.bytes,
                        elapsed_ms: 100,
                    });
                }
                Task::Done => break,
                task => panic!("重传不能停滞: {task:?}"),
            }
        }
        retried.sort_unstable();
        let mut expected: Vec<_> = retired.iter().map(|t| t.index).collect();
        expected.sort_unstable();
        assert_eq!(retried, expected);
    }

    #[test]
    fn all_transfer_nodes_unavailable_fails_instead_of_waiting() {
        let p = Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap();
        let mut p = run_probes(p, &[]);
        p.begin_transfer("u1", &[0]);
        let Task::Chunk(t) = p.next_task() else {
            panic!("应派发首片")
        };
        p.report(Report::ChunkUnavailable { seq: t.seq });
        // 重算/续传也不能把已失效的主节点 bootstrap 回调度池。
        p.resume("u2", &[0]);
        assert!(matches!(p.next_task(), Task::Failed { .. }));
    }

    fn node(id: &str, parent: Option<&str>) -> PlannerNodeConfig {
        PlannerNodeConfig {
            id: id.into(),
            url: format!("https://{id}.example/file"),
            parent: parent.map(|p| p.into()),
        }
    }

    fn input(nodes: Vec<PlannerNodeConfig>, main: &str, now: i64) -> PlannerInput {
        PlannerInput {
            total_size: 500 << 20,
            main_node: main.into(),
            now_ms: now,
            nodes,
            state: PersistedState::default(),
            adjust_cooldown_ms: 0,
            weight_exponent: DEFAULT_WEIGHT_EXPONENT,
        }
    }

    /// 跑完探测阶段。throughput_elapsed 控制吞吐样本耗时（决定测出的带宽档位）。
    fn run_probes_with(mut p: Planner, fail: &[&str], throughput_elapsed: u64) -> Planner {
        let mut guard = 0;
        loop {
            guard += 1;
            assert!(guard < 100, "探测阶段死循环");
            match p.next_task() {
                Task::Probe(t) => {
                    if fail.contains(&t.node.as_str()) {
                        p.report(Report::ProbeFailed {
                            node: t.node,
                            kind: t.kind,
                        });
                    } else {
                        let (elapsed, bytes) = match t.kind {
                            ProbeKind::Tiny => (20, 0u64),
                            ProbeKind::Throughput => (throughput_elapsed, PROBE_THROUGHPUT_BYTES),
                        };
                        p.report(Report::ProbeOk {
                            node: t.node,
                            kind: t.kind,
                            bytes,
                            elapsed_ms: elapsed,
                            backend_node: "backend-1".into(),
                        });
                    }
                }
                Task::Wait => break,
                other => panic!("探测阶段意外任务: {:?}", other),
            }
        }
        p
    }

    fn run_probes(p: Planner, fail: &[&str]) -> Planner {
        run_probes_with(p, fail, 1600)
    }

    fn chunk_or_panic(t: Task) -> ChunkTask {
        match t {
            Task::Chunk(t) => t,
            other => panic!("应派分片，got {:?}", other),
        }
    }

    #[test]
    fn test_pick_chunk_size_by_size() {
        // 默认 4MiB，与文件大小无关（带宽交给运行期 AIMD）
        assert_eq!(pick_chunk_size_for(500 << 20), DEFAULT_UPLOAD_CHUNK);
        assert_eq!(pick_chunk_size_for(1 << 20), DEFAULT_UPLOAD_CHUNK);
        assert_eq!(pick_chunk_size_for(0), DEFAULT_UPLOAD_CHUNK);
        // 片数封顶：100GiB 在默认档会超 MAX_CHUNKS → 自动抬档直到放得下
        let total = 100u64 << 30;
        let c = pick_chunk_size_for(total);
        assert!(c > DEFAULT_UPLOAD_CHUNK);
        assert!(total.div_ceil(c) <= MAX_CHUNKS as u64 || c == MAX_CHUNK);
    }

    #[test]
    fn test_initial_window() {
        assert_eq!(initial_window(8e6, Duration::from_millis(20), 8 << 20), 1);
        assert_eq!(initial_window(16e6, Duration::from_millis(100), 1 << 20), 2);
        // 300Mbps 单路径 → 38 路封顶 32
        assert_eq!(
            initial_window(300e6, Duration::from_millis(5), MAX_CHUNK),
            MAX_PER_NODE_CONCURRENCY
        );
        assert_eq!(initial_window(0.0, Duration::from_millis(10), MAX_CHUNK), 1);
    }

    #[test]
    fn test_mem_budget_caps_window() {
        // 超大带宽探针（2MiB/5ms ≈ 419MB/s）→ 初始窗口需求巨大，
        // 必须被内存预算钳到 64MB/4MB=16，而不是 MAX_PER_NODE_CONCURRENCY(32)
        let nodes = vec![node("main", None)];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes_with(p, &[], 5);
        let chunk = p.planned_chunk_size().unwrap();
        assert_eq!(chunk, DEFAULT_UPLOAD_CHUNK);
        let w = p.windows()[0].1;
        assert_eq!(w, (MEM_BUDGET_PER_NODE / chunk) as usize);
        assert!(w < MAX_PER_NODE_CONCURRENCY);
    }

    #[test]
    fn test_chunk_timeout_clamp() {
        assert_eq!(
            chunk_timeout(Duration::from_millis(10), Some(100e6), MAX_CHUNK),
            TIMEOUT_MIN
        );
        assert_eq!(
            chunk_timeout(Duration::from_secs(1), Some(1e3), MAX_CHUNK),
            TIMEOUT_MAX
        );
        let t = chunk_timeout(Duration::from_millis(100), Some(400e3), 4 << 20);
        assert!(t > TIMEOUT_MIN && t < TIMEOUT_MAX);
        // 无吞吐 EMA（首片）：给足上限时间，慢链路首片不被误判拥塞
        assert_eq!(
            chunk_timeout(Duration::from_millis(20), None, DEFAULT_UPLOAD_CHUNK),
            TIMEOUT_MAX
        );
    }

    #[test]
    fn test_combine_effective_parent_cap() {
        let nodes = vec![
            node("main", None),
            node("hk", Some("main")),
            node("leaf", Some("hk")),
        ];
        let mut m = HashMap::new();
        m.insert("main".to_string(), 100e6);
        m.insert("hk".to_string(), 80e6);
        m.insert("leaf".to_string(), 50e6);
        let eff = combine_effective(&nodes, &m);
        assert_eq!(eff["main"], 100e6);
        assert_eq!(eff["hk"], 80e6);
        // leaf 经 hk 转发：自身 50 < hk 80 → 50
        assert_eq!(eff["leaf"], 50e6);
        // 父只有 20：子封顶 20
        let mut m2 = HashMap::new();
        m2.insert("main".to_string(), 20e6);
        m2.insert("hk".to_string(), 80e6);
        let eff2 = combine_effective(&nodes, &m2);
        assert_eq!(eff2["hk"], 20e6);
    }

    #[test]
    fn test_probe_order_main_first_parent_before_child() {
        let nodes = vec![
            node("leaf", Some("hk")),
            node("hk", Some("main")),
            node("main", None),
            node("other", None),
        ];
        let order = probe_order(&nodes, "main");
        assert_eq!(order[0], "main");
        let pos = |id: &str| order.iter().position(|x| x == id).unwrap();
        assert!(pos("hk") < pos("leaf"));
        assert!(pos("main") < pos("hk"));
    }

    #[test]
    fn test_full_transfer_multi_node() {
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes(p, &[]);

        let chunk = p.planned_chunk_size().unwrap();
        assert!((MIN_CHUNK..=MAX_CHUNK).contains(&chunk));
        let windows = p.windows();
        assert_eq!(windows.len(), 2);
        assert!(windows.iter().all(|(_, w)| *w >= 1 && *w <= 32));

        // 驱动到完成：每派一片立即回报成功，统计节点使用
        p.begin_transfer("u1", &[0, 1, 2, 3, 4, 5]);
        let mut stats: HashMap<String, usize> = HashMap::new();
        let mut guard = 0;
        loop {
            guard += 1;
            assert!(guard < 10000, "传输阶段死循环");
            match p.next_task() {
                Task::Chunk(t) => {
                    *stats.entry(t.node.clone()).or_default() += 1;
                    p.report(Report::ChunkOk {
                        seq: t.seq,
                        bytes: t.bytes,
                        elapsed_ms: 500,
                    });
                }
                Task::Done => break,
                Task::Failed { reason } => panic!("不应失败: {}", reason),
                Task::Wait => panic!("每派即报不应出现 Wait"),
                other => panic!("意外任务: {:?}", other),
            }
        }
        assert!(
            stats.get("main").copied().unwrap_or(0) >= 1,
            "主节点应被用到"
        );
        assert!(stats.get("hk").copied().unwrap_or(0) >= 1, "hk 应被用到");
    }

    #[test]
    fn test_probes_run_during_transfer() {
        // 边传边测：不再需要先跑探测，begin_transfer 后主节点按启动窗口立即发片；
        // 窗口占满时顺手把后台探测派出去。
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let mut p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let chunk = p.planned_chunk_size().unwrap();
        assert_eq!(chunk, DEFAULT_UPLOAD_CHUNK);

        p.begin_transfer("u1", &(0..8).collect::<Vec<_>>());
        let mut pending: Vec<ChunkTask> = Vec::new();
        let mut chunks = 0;
        let mut probes = 0;
        let mut guard = 0;
        loop {
            guard += 1;
            assert!(guard < 1000, "死循环");
            match p.next_task() {
                Task::Chunk(t) => {
                    chunks += 1;
                    pending.push(t); // 先攒着不回报，模拟在途以逼出窗口占满
                    if pending.len() >= 4 {
                        let t = pending.remove(0);
                        p.report(Report::ChunkOk {
                            seq: t.seq,
                            bytes: t.bytes,
                            elapsed_ms: 100,
                        });
                    }
                }
                Task::Probe(t) => {
                    probes += 1;
                    let (elapsed, bytes) = match t.kind {
                        ProbeKind::Tiny => (20, 0u64),
                        ProbeKind::Throughput => (1600, PROBE_THROUGHPUT_BYTES),
                    };
                    p.report(Report::ProbeOk {
                        node: t.node,
                        kind: t.kind,
                        bytes,
                        elapsed_ms: elapsed,
                        backend_node: "backend-1".into(),
                    });
                }
                Task::Done => break,
                Task::Failed { reason } => panic!("不应失败: {}", reason),
                Task::Reinit => panic!("不应重 init"),
                Task::Wait => {
                    let t = pending
                        .first()
                        .cloned()
                        .expect("Wait 时既无任务又无在途分片");
                    pending.remove(0);
                    p.report(Report::ChunkOk {
                        seq: t.seq,
                        bytes: t.bytes,
                        elapsed_ms: 100,
                    });
                }
            }
        }
        assert_eq!(chunks, 8);
        assert!(probes >= 1, "传输期应把后台探测也派出去");
    }

    #[test]
    fn test_drr_weight_exponent_balances_links() {
        // 两节点带宽悬殊（main ≈21MB/s、hk ≈0.5MB/s），验证 weight_exponent 对分片分配的影响。
        let run = |exp: f64| -> (usize, usize) {
            let nodes = vec![node("main", None), node("hk", Some("main"))];
            let mut inp = input(nodes, "main", 1_000_000);
            inp.weight_exponent = exp;
            let mut p = Planner::new(inp).unwrap();
            loop {
                match p.next_task() {
                    Task::Probe(t) => {
                        let (elapsed, bytes) = if t.kind == ProbeKind::Throughput {
                            (
                                if t.node == "main" { 100 } else { 4000 },
                                PROBE_THROUGHPUT_BYTES,
                            )
                        } else {
                            (20, 0u64)
                        };
                        p.report(Report::ProbeOk {
                            node: t.node,
                            kind: t.kind,
                            bytes,
                            elapsed_ms: elapsed,
                            backend_node: "backend-1".into(),
                        });
                    }
                    Task::Wait => break,
                    other => panic!("探测阶段意外: {:?}", other),
                }
            }
            p.begin_transfer("u1", &(0..60).collect::<Vec<_>>());
            let mut counts: HashMap<String, usize> = HashMap::new();
            let mut guard = 0;
            loop {
                guard += 1;
                assert!(guard < 100000, "死循环");
                match p.next_task() {
                    Task::Chunk(t) => {
                        *counts.entry(t.node.clone()).or_default() += 1;
                        p.report(Report::ChunkOk {
                            seq: t.seq,
                            bytes: t.bytes,
                            elapsed_ms: 50,
                        });
                    }
                    Task::Done => break,
                    Task::Failed { reason } => panic!("不应失败: {}", reason),
                    Task::Reinit => panic!("不应 reinit"),
                    Task::Probe(t) => panic!("探测已在 begin 前派完: {:?}", t),
                    Task::Wait => panic!("每派即报不应 Wait"),
                }
            }
            (
                counts.get("main").copied().unwrap_or(0),
                counts.get("hk").copied().unwrap_or(0),
            )
        };
        let (_, h0) = run(0.0);
        let (_, h1) = run(1.0);
        // exponent=0（均分）：慢链路也拿到接近一半
        assert!(h0 >= 24, "exponent=0 慢链路应接近均分: hk={}", h0);
        // exponent=1（严格按带宽）：慢链路只拿到零头
        assert!(h1 <= 8, "exponent=1 慢链路应被压缩: hk={}", h1);
    }

    #[test]
    fn test_congestion_halves_window_and_requeues() {
        // 大吞吐探针（2MiB/100ms ≈ 21MB/s → 初始窗口 ≥2）才能测出减半
        let nodes = vec![node("main", None)];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes_with(p, &[], 100);
        let w0 = p.windows()[0].1;
        assert!(w0 >= 2, "w0={}", w0);

        p.begin_transfer("u1", &[0, 1]);
        let t1 = chunk_or_panic(p.next_task());
        let t2 = chunk_or_panic(p.next_task());
        p.report(Report::ChunkCongested { seq: t1.seq });
        p.report(Report::ChunkCongested { seq: t2.seq });
        assert_eq!(p.windows()[0].1, (w0 / 2).max(1));
        // 两片都应重排（预算 3 → 剩 1）
        let t3 = chunk_or_panic(p.next_task());
        assert!(t3.index == t1.index || t3.index == t2.index);
    }

    #[test]
    fn test_session_gone_triggers_reinit_then_resume() {
        let nodes = vec![node("main", None)];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes(p, &[]);
        p.begin_transfer("u1", &[0, 1]);
        let t1 = chunk_or_panic(p.next_task());
        p.report(Report::ChunkSessionGone { seq: t1.seq });
        match p.next_task() {
            Task::Reinit => {}
            other => panic!("应要求 reinit: {:?}", other),
        }
        p.resume("u2", &[0, 1]);
        match p.next_task() {
            Task::Chunk(t) => assert_eq!(t.node, "main"),
            other => panic!("resume 后应继续派: {:?}", other),
        }
    }

    #[test]
    fn test_retry_exhaustion_fails() {
        let nodes = vec![node("main", None)];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes(p, &[]);
        p.begin_transfer("u1", &[0]);
        // 预算 3 次：3 次数据错误后 Failed
        for _ in 0..CHUNK_ATTEMPTS {
            match p.next_task() {
                Task::Chunk(t) => p.report(Report::ChunkDataError { seq: t.seq }),
                other => panic!("应派发: {:?}", other),
            }
        }
        match p.next_task() {
            Task::Failed { reason } => assert!(reason.contains("重试耗尽")),
            other => panic!("应失败: {:?}", other),
        }
    }

    #[test]
    fn test_all_probe_failed_bootstraps_main() {
        // 探测全失败也不再直接判死：主节点（init 锚点，已证明可达）先给启动窗口，
        // 靠运行期 AIMD/重试兜底；其余测不出/不符的节点出池。
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let p = run_probes(p, &["main", "hk"]);
        let w = |id: &str| p.windows().into_iter().find(|(x, _)| x == id).unwrap().1;
        assert!(w("main") >= 1, "主节点应 bootstrap 出启动窗口");
        assert_eq!(w("hk"), 0, "探测失败的子节点应出池");
    }

    #[test]
    fn test_backend_mismatch_excluded() {
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let p = run_probes(p, &[]);
        // 人为制造不一致：把 hk 的吞吐结果换成另一个 backend_node
        // （run_probes 已跑完，直接重新构造场景：main 有新鲜状态、hk 现场探测）
        let mut inp = input(vec![node("main", None)], "main", 2_000_000);
        inp.state = p.export_state(); // main 新鲜
        inp.nodes = vec![node("main", None), node("hk", Some("main"))];
        let p2 = Planner::new(inp).unwrap();
        // hk 探测响应 backend 不同 → 出池
        let mut guard = 0;
        let mut p2 = p2;
        loop {
            guard += 1;
            assert!(guard < 50);
            match p2.next_task() {
                Task::Probe(t) => {
                    let backend = if t.node == "hk" {
                        "evil-other"
                    } else {
                        "backend-1"
                    };
                    let (elapsed, bytes) = match t.kind {
                        ProbeKind::Tiny => (20, 0u64),
                        ProbeKind::Throughput => (1600, PROBE_THROUGHPUT_BYTES),
                    };
                    p2.report(Report::ProbeOk {
                        node: t.node.clone(),
                        kind: t.kind,
                        bytes,
                        elapsed_ms: elapsed,
                        backend_node: backend.into(),
                    });
                }
                Task::Wait => break,
                other => panic!("意外: {:?}", other),
            }
        }
        let windows = p2.windows();
        assert_eq!(windows.iter().find(|(id, _)| id == "hk").unwrap().1, 0);
        assert!(windows.iter().find(|(id, _)| id == "main").unwrap().1 >= 1);
    }

    #[test]
    fn cached_identity_and_timestamp_survive_reuse() {
        let p = run_probes(
            Planner::new(input(
                vec![node("main", None), node("other", None)],
                "main",
                1_000_000,
            ))
            .unwrap(),
            &[],
        );
        let mut i = input(
            vec![node("main", None), node("other", None)],
            "main",
            1_100_000,
        );
        i.state = p.export_state();
        i.state
            .nodes
            .iter_mut()
            .find(|s| s.id == "other")
            .unwrap()
            .backend_node = "different".into();
        let mut cached = Planner::new(i).unwrap();
        cached.begin_transfer("session", &[0, 1]);
        assert_eq!(
            cached
                .windows()
                .iter()
                .find(|(id, _)| id == "other")
                .unwrap()
                .1,
            0
        );
        let state = cached.export_state();
        assert_eq!(
            state.nodes[0].measured_at_ms, 1_000_000,
            "复用不能刷新样本时间"
        );
        assert_eq!(state.nodes[0].backend_node, "backend-1");
        let mut expired = input(vec![node("main", None)], "main", 1_700_001);
        expired.state = state;
        assert!(matches!(
            Planner::new(expired).unwrap().next_task(),
            Task::Probe(_)
        ));
    }

    #[test]
    fn cache_rejects_changed_url_future_clock_and_invalid_numbers() {
        let p = run_probes(
            Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap(),
            &[],
        );
        for case in 0..5 {
            let mut i = input(vec![node("main", None)], "main", 1_100_000);
            i.state = p.export_state();
            match case {
                0 => i.nodes[0].url = "https://changed".into(),
                1 => i.state.nodes[0].measured_at_ms = 1_200_000,
                2 => i.state.nodes[0].rtt_ms = f64::NAN,
                3 => i.state.nodes[0].bps = -1.0,
                _ => i.state.nodes[0].backend_node.clear(),
            }
            assert!(matches!(
                Planner::new(i).unwrap().next_task(),
                Task::Probe(_)
            ));
        }
    }

    #[test]
    fn secondary_waits_for_main_identity_even_when_probes_finish_first() {
        let mut p = Planner::new(input(
            vec![node("main", None), node("other", None)],
            "main",
            1_000_000,
        ))
        .unwrap();
        let mut probes = vec![];
        while let Task::Probe(t) = p.next_task() {
            probes.push(t);
        }
        for t in probes.iter().filter(|t| t.node == "other") {
            p.report(Report::ProbeOk {
                node: t.node.clone(),
                kind: t.kind,
                bytes: t.bytes,
                elapsed_ms: 20,
                backend_node: "other-backend".into(),
            });
        }
        p.begin_transfer("session", &[0, 1]);
        assert_eq!(chunk_or_panic(p.next_task()).node, "main");
        assert_eq!(
            p.windows().iter().find(|(id, _)| id == "other").unwrap().1,
            0
        );
        for t in probes.iter().filter(|t| t.node == "main") {
            p.report(Report::ProbeOk {
                node: t.node.clone(),
                kind: t.kind,
                bytes: t.bytes,
                elapsed_ms: 20,
                backend_node: "main-backend".into(),
            });
        }
        assert_eq!(
            p.windows().iter().find(|(id, _)| id == "other").unwrap().1,
            0
        );
    }

    #[test]
    fn late_throughput_cannot_revive_failed_tiny_probe() {
        let mut p = Planner::new(input(
            vec![node("main", None), node("other", None)],
            "main",
            1_000_000,
        ))
        .unwrap();
        let mut probes = vec![];
        while let Task::Probe(t) = p.next_task() {
            probes.push(t);
        }
        for t in probes.iter().filter(|t| t.node == "main") {
            p.report(Report::ProbeOk {
                node: t.node.clone(),
                kind: t.kind,
                bytes: t.bytes,
                elapsed_ms: 20,
                backend_node: "backend-1".into(),
            });
        }
        p.report(Report::ProbeFailed {
            node: "other".into(),
            kind: ProbeKind::Tiny,
        });
        p.report(Report::ProbeOk {
            node: "other".into(),
            kind: ProbeKind::Throughput,
            bytes: PROBE_THROUGHPUT_BYTES,
            elapsed_ms: 20,
            backend_node: "backend-1".into(),
        });
        assert_eq!(
            p.windows().iter().find(|(id, _)| id == "other").unwrap().1,
            0
        );
        assert!(!p.export_state().nodes.iter().any(|s| s.id == "other"));
    }

    #[test]
    fn resume_requeues_aborted_probes_and_ignores_old_chunks() {
        let mut p = Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap();
        p.begin_transfer("old", &[0]);
        assert!(matches!(p.next_task(), Task::Probe(_)));
        assert!(matches!(p.next_task(), Task::Probe(_)));
        let old = chunk_or_panic(p.next_task());
        p.report(Report::ChunkSessionGone { seq: old.seq });
        p.resume("new", &[0]);
        p.report(Report::ChunkOk {
            seq: old.seq,
            bytes: old.bytes,
            elapsed_ms: 10,
        });
        assert!(matches!(p.next_task(), Task::Probe(_)));
        assert!(p.pending.contains_key(&0));
    }

    #[test]
    fn healthy_wan_can_increase_window_when_chunk_time_exceeds_rtt() {
        let mut p = run_probes(
            Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap(),
            &[],
        );
        p.begin_transfer("session", &[0, 1, 2, 3, 4]);
        let before = p.windows()[0].1;
        for _ in 0..STREAK_TO_INCREASE {
            let t = chunk_or_panic(p.next_task());
            p.report(Report::ChunkOk {
                seq: t.seq,
                bytes: t.bytes,
                elapsed_ms: 3200,
            });
        }
        assert!(
            p.windows()[0].1 > before,
            "健康大分片不能因耗时超过 RTT 被永久锁在初始窗口"
        );
    }

    #[test]
    fn test_cabi_json_roundtrip() {
        let inp = input(vec![node("main", None)], "main", 1_000_000);
        let desc = serde_json::to_string(&inp).unwrap();
        let h = fc_planner_new(CString::new(desc).unwrap().as_ptr());
        assert!(!h.is_null());
        let t = unsafe { std::ffi::CStr::from_ptr(fc_planner_next(h)) };
        let task: Task = serde_json::from_str(t.to_str().unwrap()).unwrap();
        assert!(matches!(task, Task::Probe(_)));
        // 免费泄漏无关紧要（测试进程）
        fc_planner_free(h);
    }
}
