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
//! - 测序：主节点先测（tiny 微样本 → 吞吐样本），再按「父节点先于子节点」的顺序测其余
//!   节点；新鲜（<10min）的持久化样本直接复用不重复测。
//! - 同后端校验：测速响应里的 node 名必须等于主节点，不一致的节点剔除出上传池
//!   （用户自配节点可能指向了别的后端，多路径必须落在同一会话上）。
//! - 父链折算：子节点有效带宽 = min(自身实测, 父节点有效带宽)，共享瓶颈在此体现。
//! - 运行时：每节点独立 AIMD 窗口（上限 MAX_PER_NODE_CONCURRENCY），超时/传输错误
//!   减半，连续成功且延迟 <2×RTT 才加一；422 类数据错误不算拥塞。

use std::collections::{BTreeMap, HashMap, VecDeque};
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
}

fn default_adjust_cooldown() -> u64 {
    ADJUST_COOLDOWN_MS
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
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
    Failed { reason: String },
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

/// 按总带宽选分片档位：目标单流在途时长 ~3s，封顶 MAX_CHUNK；
/// 片数超 MAX_CHUNKS 时逐档放大，档位用完（>128GiB）就放行超 cap。
pub fn pick_chunk_size(bps_total: f64, total_size: u64) -> u64 {
    let target = (bps_total * 3.0) as u64;
    let mut pick = CHUNK_TIERS[0];
    for t in CHUNK_TIERS {
        if target >= t {
            pick = t;
        }
    }
    pick = pick.clamp(MIN_CHUNK, MAX_CHUNK);
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
/// 为什么不用教科书 BDP=带宽×RTT：分片按「3s 在途」选档后 BDP/分片 会退化成
/// RTT/3——任何正常 RTT 下都算出 1 路，32 路上限形同虚设。管道真实填充交给
/// 运行期 AIMD（它按实际成功/失败修正），这里只要给一个有区分度的起点。
pub const PER_STREAM_BPS: f64 = 8_000_000.0;

pub fn initial_window(bps: f64, _rtt: Duration, _chunk: u64) -> usize {
    if bps <= 0.0 {
        return 1;
    }
    ((bps / PER_STREAM_BPS).ceil() as usize).clamp(1, MAX_PER_NODE_CONCURRENCY)
}

/// 单片动态超时 = RTT + 2×(分片/带宽EMA)，clamp [15s, 120s]。
pub fn chunk_timeout(rtt: Duration, bps_ema: Option<f64>, chunk: u64) -> Duration {
    let transfer = match bps_ema {
        Some(b) if b > 0.0 => Duration::from_secs_f64(2.0 * chunk as f64 / b),
        _ => Duration::from_secs(30),
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
    Probing,
    Ready,
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
    /// 探测失败（本次探测且没有新鲜持久化兜底）。
    probe_dead: bool,
    window: usize,
    deficit: f64,
    streak: u32,
    last_adjust: Option<Instant>,
    /// 传输期 EMA（bytes/s），由成功分片持续更新。
    ema_bps: Option<f64>,
}

impl NodeRuntime {
    fn effective_bps(&self) -> f64 {
        self.bps.unwrap_or(0.0)
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
    phase: Phase,
    nodes: Vec<NodeRuntime>,
    /// 探测队列：node id + kind，按测序顺序。
    probe_queue: VecDeque<(String, ProbeKind)>,
    probing: Option<(u64, String, ProbeKind)>,
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
        let main = input.main_node.clone();
        let cooldown = Duration::from_millis(input.adjust_cooldown_ms);
        let fresh: HashMap<String, &PersistedNodeState> = input
            .state
            .nodes
            .iter()
            .filter(|n| input.now_ms - n.measured_at_ms <= STATE_FRESH_MS)
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
                // 无新鲜样本：排两个探针任务（微样本 + 吞吐）
                probe_queue.push_back((id.clone(), ProbeKind::Tiny));
                probe_queue.push_back((id.clone(), ProbeKind::Throughput));
            }
            nodes.push(NodeRuntime {
                cfg,
                rtt,
                bps,
                backend_node: None,
                probe_dead: false,
                window: 1,
                deficit: 0.0,
                streak: 0,
                last_adjust: None,
                ema_bps: None,
            });
        }

        let phase = if probe_queue.is_empty() {
            Phase::Ready
        } else {
            Phase::Probing
        };

        Ok(Planner {
            total_size: input.total_size,
            chunk_size: 0,
            adjust_cooldown: cooldown,
            now_ms: input.now_ms,
            main_node: main,
            phase,
            nodes,
            probe_queue,
            probing: None,
            seq: 0,
            upload_id: String::new(),
            pending: BTreeMap::new(),
            dispatch_queue: VecDeque::new(),
            inflight: HashMap::new(),
            reinit_notified: false,
            fail_reason: None,
        })
    }

    /// 选片结果：begin_transfer 之前可用（probe 完成后调用；若全部节点都有新鲜样本则立即可用）。
    pub fn planned_chunk_size(&mut self) -> Option<u64> {
        if self.chunk_size == 0 {
            self.finalize_plan();
        }
        (self.chunk_size != 0).then_some(self.chunk_size)
    }

    /// 每个节点的初始窗口（调试用/日志用）。
    pub fn windows(&self) -> Vec<(String, usize)> {
        self.nodes.iter().map(|n| (n.cfg.id.clone(), n.window)).collect()
    }

    /// 探测+规划完成后，平台 describe/init 完成，开始传分片。
    pub fn begin_transfer(&mut self, upload_id: &str, missing: &[u32]) {
        if self.chunk_size == 0 {
            self.finalize_plan();
        }
        self.upload_id = upload_id.to_string();
        self.pending = missing
            .iter()
            .map(|&i| (i, CHUNK_ATTEMPTS))
            .collect();
        self.dispatch_queue = missing.iter().copied().collect();
        self.inflight.clear();
        self.phase = Phase::Transfer;
        self.reinit_notified = false;
    }

    /// 会话过期后重新接入。
    pub fn resume(&mut self, upload_id: &str, missing: &[u32]) {
        self.upload_id = upload_id.to_string();
        self.pending = missing.iter().map(|&i| (i, CHUNK_ATTEMPTS)).collect();
        self.dispatch_queue = missing.iter().copied().collect();
        self.inflight.clear();
        self.phase = Phase::Transfer;
        self.reinit_notified = false;
    }

    pub fn next_task(&mut self) -> Task {
        match self.phase {
            Phase::Finished => match &self.fail_reason {
                Some(r) => Task::Failed { reason: r.clone() },
                None => Task::Done,
            },
            Phase::Probing => self.next_probe_task(),
            Phase::Ready => {
                self.finalize_plan();
                Task::Wait
            }
            Phase::NeedReinit => {
                if !self.reinit_notified {
                    self.reinit_notified = true;
                    Task::Reinit
                } else {
                    Task::Wait
                }
            }
            Phase::Transfer => self.next_chunk_task(),
        }
    }

    /// 平台循环的调试/日志辅助：当前所处阶段名。
    pub fn phase_name(&self) -> &'static str {
        match self.phase {
            Phase::Probing => "probing",
            Phase::Ready => "ready",
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
                    let rtt = n.rtt?;
                    let bps = n.bps?;
                    Some(PersistedNodeState {
                        id: n.cfg.id.clone(),
                        rtt_ms: rtt.as_secs_f64() * 1000.0,
                        bps,
                        measured_at_ms: self.now_ms,
                    })
                })
                .collect(),
        }
    }

    // ---- 探测 ----

    fn next_probe_task(&mut self) -> Task {
        if self.probing.is_some() {
            // 上一轮探测还没回结果：等 report 驱动，绝不重复派发同一探测
            return Task::Wait;
        }
        match self.probe_queue.pop_front() {
            Some((node, kind)) => {
                self.seq += 1;
                self.probing = Some((self.seq, node.clone(), kind));
                Task::Probe(ProbeTask {
                    seq: self.seq,
                    node: node.clone(),
                    url: self.node_url(&node),
                    kind,
                    bytes: probe_bytes(kind),
                })
            }
            None => {
                self.finalize_plan();
                Task::Wait
            }
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
        if let Some((_, pn, pk)) = &self.probing {
            if pn != node || *pk != kind {
                return; // 乱序/过期探测结果，忽略
            }
        } else {
            return;
        }
        self.probing = None;
        let n = match self.node_mut(node) {
            Some(n) => n,
            None => return,
        };
        n.backend_node = Some(backend_node.to_string());
        if backend_node.is_empty() {
            n.probe_dead = true;
            return;
        }
        let elapsed = Duration::from_millis(elapsed_ms.max(1));
        match kind {
            ProbeKind::Tiny => {
                n.rtt = Some(elapsed);
            }
            ProbeKind::Throughput => {
                if bytes > 0 {
                    n.bps = Some(bytes as f64 / elapsed.as_secs_f64());
                }
            }
        }
    }

    fn on_probe_failed(&mut self, node: &str, kind: ProbeKind) {
        if let Some((_, pn, pk)) = &self.probing {
            if pn != node || *pk != kind {
                return;
            }
        } else {
            return;
        }
        self.probing = None;
        // tiny 失败说明节点不可达：后续探测也没意义，清空队列里它的任务
        if kind == ProbeKind::Tiny {
            self.probe_queue.retain(|(id, _)| id != node);
            if let Some(n) = self.node_mut(node) {
                n.probe_dead = true;
            }
        }
    }

    fn finalize_plan(&mut self) {
        if self.chunk_size != 0 || self.phase == Phase::Finished {
            return;
        }
        let main_backend = self
            .nodes
            .iter()
            .find(|n| n.cfg.id == self.main_id())
            .and_then(|n| n.backend_node.clone())
            .unwrap_or_default();

        // 同后端校验：主节点有 backend_node 时，不一致的剔除
        for n in &mut self.nodes {
            if n.probe_dead && n.rtt.is_none() {
                n.bps = None;
                continue;
            }
            if let Some(b) = &n.backend_node {
                if !main_backend.is_empty() && b != &main_backend {
                    n.bps = None; // 指向了别的后端，出池
                }
            }
        }

        let cfgs: Vec<PlannerNodeConfig> =
            self.nodes.iter().map(|n| n.cfg.clone()).collect();
        let measured: HashMap<String, f64> = self
            .nodes
            .iter()
            .filter(|n| n.rtt.is_some())
            .map(|n| (n.cfg.id.clone(), n.effective_bps()))
            .collect();
        let eff = combine_effective(&cfgs, &measured);

        let total: f64 = eff.values().sum();
        if total <= 0.0 {
            self.phase = Phase::Finished;
            self.fail_reason = Some("没有可用路径（全部节点测速失败）".into());
            return;
        }

        self.chunk_size = pick_chunk_size(total, self.total_size);
        for n in &mut self.nodes {
            match (eff.get(&n.cfg.id), n.rtt) {
                (Some(&e), Some(rtt)) if e > 0.0 => {
                    n.window = initial_window(e, rtt, self.chunk_size);
                    n.bps = Some(e);
                }
                _ => {
                    n.window = 0; // 出池
                }
            }
            n.deficit = 0.0;
        }
        self.phase = Phase::Ready;
    }

    // ---- 传输 ----

    fn next_chunk_task(&mut self) -> Task {
        if self.pending.is_empty() && self.inflight.is_empty() {
            self.phase = Phase::Finished;
            return Task::Done;
        }
        // 从派发队列取还有预算的片
        let index = loop {
            match self.dispatch_queue.pop_front() {
                None => return Task::Wait,
                Some(i) => {
                    if self.pending.contains_key(&i) {
                        break i;
                    }
                }
            }
        };
        // DRR（赤字轮询）：deficit 最大且有空窗的节点胜出；
        // 分派后 deficit += 自身权重 - 总权重（允许为负），带宽占比大的节点自然多拿。
        let weight = |n: &NodeRuntime| n.effective_bps().max(1.0);
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
                return Task::Wait; // 全部窗口占满
            }
        };
        self.seq += 1;
        let seq = self.seq;
        {
            let n = &mut self.nodes[ni];
            n.deficit += weight(n) - total_weight;
        }
        let node = &self.nodes[ni];
        let timeout = chunk_timeout(node.rtt.unwrap_or(Duration::from_secs(1)), node.ema_bps, self.chunk_size);
        self.inflight.insert(
            seq,
            Inflight {
                node: node.cfg.id.clone(),
                index,
            },
        );
        Task::Chunk(ChunkTask {
            seq,
            node: node.cfg.id.clone(),
            url: node.cfg.url.clone(),
            index,
            timeout_ms: timeout.as_millis() as u64,
            bytes: self.chunk_size,
        })
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
        self.inflight.remove(&seq);
        if self.phase == Phase::Transfer {
            self.phase = Phase::NeedReinit;
            self.reinit_notified = false;
        }
    }

    fn maybe_increase_window(&mut self, node_id: &str) {
        let cooldown = self.adjust_cooldown;
        let chunk = self.chunk_size;
        let (streak, window, last_adjust, baseline, ema_bps) = {
            let n = match self.node_mut(node_id) {
                Some(n) => n,
                None => return,
            };
            (
                n.streak,
                n.window,
                n.last_adjust,
                n.rtt.unwrap_or(Duration::from_secs(1)),
                n.ema_bps,
            )
        };
        if streak < STREAK_TO_INCREASE || window >= MAX_PER_NODE_CONCURRENCY {
            return;
        }
        let cooldown_ok = last_adjust
            .map(|t| t.elapsed() >= cooldown)
            .unwrap_or(true);
        if !cooldown_ok {
            return;
        }
        // 平均片延迟须 < 2×RTT 基线才加窗
        let avg = match ema_bps {
            Some(b) if b > 0.0 => Duration::from_secs_f64(chunk as f64 / b),
            _ => Duration::ZERO,
        };
        if avg <= baseline.mul_f64(2.0) {
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
    let mut remaining: Vec<&PlannerNodeConfig> =
        nodes.iter().filter(|n| n.id != main).collect();
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
        Ok(s) => CString::new(s).map(|c| c.into_raw()).unwrap_or(std::ptr::null_mut()),
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
            Err(_) => return json_string(&Task::Failed {
                reason: "无效句柄".into(),
            }),
        };
        json_string(&p.next_task())
    }));
    r.unwrap_or_else(|_| json_string(&Task::Failed {
        reason: "panic".into(),
    }))
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
    fn test_pick_chunk_size_tiers() {
        assert_eq!(pick_chunk_size(30e3, 500 << 20), MIN_CHUNK); // 极慢网 → 最低档
        assert_eq!(pick_chunk_size(1e6, 500 << 20), 2 << 20); // 1MB/s×3s=3MB → 2MB档
        assert_eq!(pick_chunk_size(10e6, 500 << 20), 8 << 20); // 30MB → 封顶
        assert_eq!(pick_chunk_size(3e6, 500 << 20), 8 << 20); // 9MB → 8MB档
        // 片数封顶：2GiB 文件在最低档会超 16384 片 → 自动抬档直到放得下
        let c = pick_chunk_size(1e6, 2 << 30);
        assert!((2u64 << 30) / c <= MAX_CHUNKS as u64 || c == MAX_CHUNK);
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
        assert!(stats.get("main").copied().unwrap_or(0) >= 1, "主节点应被用到");
        assert!(stats.get("hk").copied().unwrap_or(0) >= 1, "hk 应被用到");
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
    fn test_all_probe_failed() {
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes(p, &["main", "hk"]);
        match p.next_task() {
            Task::Failed { reason } => assert!(reason.contains("没有可用路径")),
            other => panic!("应失败: {:?}", other),
        }
    }

    #[test]
    fn test_backend_mismatch_excluded() {
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
        let mut p = run_probes(p, &[]);
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
                    let backend = if t.node == "hk" { "evil-other" } else { "backend-1" };
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
        };
        let windows = p2.windows();
        assert_eq!(windows.iter().find(|(id, _)| id == "hk").unwrap().1, 0);
        assert!(windows.iter().find(|(id, _)| id == "main").unwrap().1 >= 1);
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
