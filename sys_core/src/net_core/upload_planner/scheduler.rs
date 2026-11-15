use std::collections::{BTreeMap, HashMap, HashSet, VecDeque};
use std::time::{Duration, Instant};

use super::{
    chunk_timeout, combine_effective, initial_window, pick_chunk_size_for, probe_bytes,
    probe_order, ChunkTask, PersistedNodeState, PersistedState, PlannerInput, PlannerNodeConfig,
    ProbeKind, ProbeTask, Report, Task, BOOTSTRAP_WINDOW, CHUNK_ATTEMPTS, MAX_CONCURRENT_PROBES,
    MAX_PER_NODE_CONCURRENCY, MEM_BUDGET_PER_NODE, STATE_FRESH_MS, STREAK_TO_INCREASE,
};

const RECOVERY_DELAY: Duration = Duration::from_secs(1);

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
    /// 从恢复 Tiny 到吞吐探测结束的节点；吞吐失败时须重新进入恢复等待。
    recovering: HashSet<String>,
    /// 仅在全节点出池时设置；重复 Recover 不得重置该截止时间。
    recovery_at: Option<Instant>,
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
            recovering: HashSet::new(),
            recovery_at: None,
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

    /// 最终校验定位到坏块后，重新 init 获取缺片；由平台限制恢复次数。
    pub fn request_reinit(&mut self) {
        self.phase = Phase::NeedReinit;
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
            self.queue_recovery_probes_if_due();
            if let Some(t) = self.take_probe_task() {
                return Task::Probe(t);
            }
            if let Some(t) = self.take_chunk_task() {
                return Task::Chunk(t);
            }
            if !self.pending.is_empty()
                && self.inflight.is_empty()
                && self.probing.is_empty()
                && self.nodes.iter().all(|n| n.window == 0)
            {
                if self.nodes.iter().any(|n| n.transfer_dead || n.probe_dead) {
                    let now = Instant::now();
                    let recovery_at = *self.recovery_at.get_or_insert_with(|| now + RECOVERY_DELAY);
                    return Task::Recover {
                        after_ms: recovery_at.saturating_duration_since(now).as_millis() as u64,
                    };
                }
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

    fn queue_recovery_probes_if_due(&mut self) {
        if self
            .recovery_at
            .map(|at| Instant::now() < at)
            .unwrap_or(true)
        {
            return;
        }
        self.recovery_at = None;
        let dead: Vec<String> = self
            .nodes
            .iter()
            .filter(|n| n.transfer_dead || n.probe_dead)
            .map(|n| n.cfg.id.clone())
            .collect();
        for node in dead {
            let queued = self
                .probe_queue
                .iter()
                .any(|(id, kind)| id == &node && *kind == ProbeKind::Tiny);
            if !queued && !self.probing.contains(&(node.clone(), ProbeKind::Tiny)) {
                self.probe_queue.push_back((node.clone(), ProbeKind::Tiny));
                self.recovering.insert(node);
            }
        }
    }

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
        let recovering = self.recovering.contains(node);
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
                        // 恢复连通性后先解除出池标记；主节点可 bootstrap，副节点仍须吞吐样本。
                        n.transfer_dead = false;
                        n.probe_dead = false;
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
        if kind == ProbeKind::Tiny && recovering && !backend_node.is_empty() {
            let queued = self
                .probe_queue
                .iter()
                .any(|(id, k)| id == node && *k == ProbeKind::Throughput);
            if !queued
                && !self
                    .probing
                    .contains(&(node.to_string(), ProbeKind::Throughput))
            {
                self.probe_queue
                    .push_back((node.to_string(), ProbeKind::Throughput));
            }
        }
        if kind == ProbeKind::Throughput {
            self.recovering.remove(node);
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
        } else if self.recovering.remove(node) {
            // 恢复后的副节点没有有效吞吐样本不能入池，下一轮重新从 Tiny 探测。
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

    #[cfg(test)]
    pub(super) fn expire_recovery_for_test(&mut self) {
        self.recovery_at = Some(Instant::now());
    }

    #[cfg(test)]
    pub(super) fn has_pending_for_test(&self, index: u32) -> bool {
        self.pending.contains_key(&index)
    }
}
