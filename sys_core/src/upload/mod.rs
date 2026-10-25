// chunked_uploader.rs
// 分片上传编排：算描述信息（叶子/树根/整文件 blake3）→ init →（秒传则完成）
//               → 乱序并发补传缺失分片 → complete。
//
// 两条路径：
// - 自适应（默认）：分片大小只由文件大小决定，describe→init 立即开始（不再等探测，首字节
//   即时发出）；传输期由 planner 做多路径调度（父链折算共享瓶颈、per-node AIMD 窗口、动态
//   超时、重排重试 CHUNK_ATTEMPTS=3 全部由 planner 负责，本端不做分片级内部重试）。后台探测
//   以「闲时补测」方式与传输并发：planner 只在派不出分片时顺手给探测，用富余带宽补测其它线路，
//   样本回来后并入调度（10min 内有新鲜持久化样本则跳过）。
// - 固定覆盖（UploadOptions.chunk_size 有值）：跳过 planner，init/chunk 全走激活节点的旧
//   单路径行为，可预期。
//
// planner 是同步对象，本模块是它的平台侧：分片与探测各派一个 tokio task，结果经 channel
// 喂回本循环；Task::Wait = 无任务可分派，挂在 channel 上等在途分片/探测的 Report。
// Chunk 任务带明确 node url，必须打到该 URL（多路径必须落在同一会话上，由 planner 做
// 同后端校验）。
//
// 进度：字节级，基于已成功分片累计字节数回调；init 阶段先置「已落盘分片」估算基线。
// 取消：本函数是 async，调用方 cancel 它对应的 task 即停止派发新分片；已在途分片随循环
//   退出被 abort（Fatal 路径）或自然完成（Done 时无在途分片）。
// 会话过期：分片收 code==404 → 回报 ChunkSessionGone → planner 发 Task::Reinit → 本端用
//   同一 Description 重新 init（不重算哈希）→ planner.resume 续传。
//
// 哈希规则与 sync_core/README.md 一致：叶子=blake3(分片字节)；parent=blake3(left‖right)；
// 奇数节点原样进位；空→blake3("")；单叶子→该叶子本身。与 Android Blake3Util.kt 逐字节一致。
pub mod bridge;
mod host;
pub mod protocol;
pub use crate::file::file_blake3_hex;
use crate::file::{describe, Description};
use crate::upload_planner as planner;
pub use host::{
    Client, HostFuture, PlannerSettings, RequestBody, Response, UploadHost, UploadRequest,
};
use protocol::*;
use std::collections::HashMap;
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::mpsc;
use tokio::task::JoinSet;

/// 默认分片 4MiB（固定覆盖路径的缺省值）。
pub const DEFAULT_CHUNK_SIZE: usize = 4 * 1024 * 1024;

/// 默认并发分片数（固定覆盖路径的缺省值）。
pub const DEFAULT_CONCURRENCY: usize = 3;

/// 传输循环在 channel 与在途分片都无进展时的停滞保护：planner 的单片超时上限 120s，
/// 超过 2 倍仍无任何分片完成说明调度或执行挂死，报错退出而不是干等。
const STALL_TIMEOUT: Duration = Duration::from_secs(240);
/// 单片超过 10 秒即放弃当前链路，由调度器交给其他可用节点。
const CHUNK_FAILOVER_TIMEOUT: Duration = Duration::from_secs(10);

/// 进度回调类型：`(已发送字节, 总字节)`。Arc 以便多 task 共享。
pub type ProgressFn = Arc<dyn Fn(i64, i64) + Send + Sync + 'static>;

/// 大文件描述计算不占异步运行时线程；暂停/取消命令能够立即被处理。
async fn describe_async(file: &Path, chunk_size: usize) -> Result<Description, String> {
    let path = file.to_path_buf();
    tokio::task::spawn_blocking(move || describe(&path, chunk_size))
        .await
        .map_err(|e| format!("描述任务失败: {e}"))?
        .map_err(|e| format!("计算文件描述信息失败: {e}"))
}

#[derive(Clone)]
pub struct UploadOptions {
    /// 分片大小覆盖：None=自适应（planner 按文件大小定档 + 多路径调度 + 传输期后台探测，默认）；
    /// Some(n)=固定 n 字节分片，走激活节点的旧单路径行为。
    pub chunk_size: Option<usize>,
    /// 并发覆盖：None=planner 的 per-node AIMD 窗口；Some(n)=固定协程池（固定路径用）。
    pub concurrency: Option<usize>,
    /// 本机设备 id，服务端派发同步任务时排除源设备。
    pub device_id: Arc<str>,
    /// 目标同名时的策略：空/"reject" = 报错（默认，同步链路必须用这个），
    /// "timestamp" = 服务端自动给文件名加时间戳区分（发布 APK 这类同名是常态的场景）。
    pub on_conflict: Arc<str>,
    /// 同步链路的覆盖式上传：Some(base_hash) 表示带基线上传（"" = 本机没有基线），
    /// 服务端目标已存在时据此判断是快进还是与其它设备分叉；None = 普通上传。
    pub sync_base: Option<Arc<str>>,
    /// 粘贴快传：init 带 quick_share=true（服务端自算目标路径、按快传配额预检），
    /// complete 返回分享记录（QuickShareCompleteData）而非文件记录。
    pub quick_share: bool,
}

/// init 被服务端以版本冲突拒绝时，错误信息里带的标记。服务端已把冲突登记为待办并通知本机隔离本地副本，
/// 调用方见到它不要当普通失败重试。
pub const SYNC_CONFLICT: &str = "同步冲突";

impl UploadOptions {
    pub fn new(device_id: impl Into<String>) -> Self {
        UploadOptions {
            chunk_size: None,
            concurrency: None,
            device_id: Arc::from(device_id.into().into_boxed_str()),
            on_conflict: Arc::from(""),
            sync_base: None,
            quick_share: false,
        }
    }

    /// 标记为同步链路的覆盖式上传，并带上本机已知的基线 hash（没有基线传空串）。
    pub fn with_sync_base(mut self, base_hash: impl AsRef<str>) -> Self {
        self.sync_base = Some(Arc::from(base_hash.as_ref()));
        self
    }

    /// 粘贴快传模式：complete 返回分享记录，目标路径/配额由服务端按 quick_share 规则处理。
    pub fn with_quick_share(mut self) -> Self {
        self.quick_share = true;
        self
    }

    /// 同名自动加时间戳（不影响其它字段）。
    pub fn with_timestamp_on_conflict(mut self) -> Self {
        self.on_conflict = Arc::from("timestamp");
        self
    }

    /// 显式固定分片大小与并发数，跳过 planner 的自适应路径（保留旧单路径行为的开关）。
    pub fn with_fixed_chunks(mut self, chunk_size: usize, concurrency: usize) -> Self {
        self.chunk_size = Some(chunk_size);
        self.concurrency = Some(concurrency);
        self
    }
}

impl Default for UploadOptions {
    fn default() -> Self {
        Self::new("")
    }
}

/// 上传整份文件。
/// `on_progress(sent_bytes, total_bytes)` 在 init 后及每片成功后回调。
/// `config` 提供节点池 / 激活节点 / planner 持久化状态（自适应路径用；固定路径不读）。
pub async fn upload(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &PlannerSettings,
) -> Result<UploadCompleteData, String> {
    let v = upload_inner(client, file, remote_dir, options, on_progress, config).await?;
    serde_json::from_value(v).map_err(|e| format!("解析上传结果失败: {}", e))
}

/// 粘贴快传上传：与 upload() 同一条多路管道，complete 返回分享记录
/// （服务端 finalizeShareLink 的 data 原样，与旧单发快传接口形状一致）。
pub async fn quick_share_upload(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &PlannerSettings,
) -> Result<QuickShareCompleteData, String> {
    let session_client = client.for_upload_session();
    let v = upload_inner(
        &session_client,
        file,
        remote_dir,
        options,
        on_progress,
        config,
    )
    .await?;
    let mut data: QuickShareCompleteData =
        serde_json::from_value(v).map_err(|e| format!("解析分享结果失败: {}", e))?;
    if data.entry_candidates.is_empty() {
        data.entry_candidates
            .push(session_client.server_url().to_string());
    }
    data.entry_candidates
        .sort_by_key(|url| !url.starts_with("https://"));
    for base in &data.entry_candidates {
        let url = format!("{}{}", base.trim_end_matches('/'), data.url_path);
        if session_client.probe_share_url(&url).await.is_ok() {
            data.entry_url = base.trim_end_matches('/').to_string();
            return Ok(data);
        }
    }
    Err("文件已完成，但所有分享入口均不可下载；未复制失效链接".into())
}

/// upload() / quick_share_upload() 共用的传输主线：按大小定档 → describe → init →
/// 多路调度传片（后台探测并发补测）→ complete，返回 complete 响应的原始 JSON，由调用方按模式解析。
/// quick_share 时 init 带 quick_share=true，complete 的 data 是分享记录结构。
async fn upload_inner(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &PlannerSettings,
) -> Result<serde_json::Value, String> {
    if let Some(chunk_size) = options.chunk_size {
        // 固定覆盖路径：完全走旧单路径行为（describe→init→激活节点并发补传→complete，
        // 含 SessionGone 重 init 一次），不探测、不多路径。
        let desc = describe_async(file, chunk_size).await?;
        on_progress(0, desc.total_size);
        match run_once(
            client,
            file,
            remote_dir,
            &desc,
            options,
            on_progress.clone(),
        )
        .await
        {
            Ok(d) => Ok(d),
            Err(e) if e.is_session_gone() => {
                // 会话过期：重新 init 整流程一次（不重算哈希）
                run_once(
                    client,
                    file,
                    remote_dir,
                    &desc,
                    options,
                    on_progress.clone(),
                )
                .await
                .map_err(|e2| e2.into_string())
            }
            Err(e) => Err(e.into_string()),
        }
    } else {
        // 自适应路径：按大小定档 + 多路径调度（后台探测并发补测）
        run_planned(client, file, remote_dir, options, on_progress, config).await
    }
}

enum UploadError {
    /// 普通错误，message 即可
    Other(String),
    /// 会话过期，外层重新 init 一次
    SessionGone,
}

impl UploadError {
    fn is_session_gone(&self) -> bool {
        matches!(self, UploadError::SessionGone)
    }
    fn into_string(self) -> String {
        match self {
            UploadError::Other(s) => s,
            UploadError::SessionGone => "会话已过期".into(),
        }
    }
}

/// 平台侧执行结果：正常 Report 喂回 planner；Fatal 是平台级失败（切节点作废 / 读文件失败），
/// 没法也不应交给 planner 重排，整趟上传直接失败，交给上层决策（如换新节点重试）。
enum Outcome {
    Report(planner::Report),
    Fatal(String),
}

/// 测速接口响应 data 段（/v1/net/speedtest/upload）。
#[derive(Debug, serde::Deserialize, Default)]
#[allow(dead_code)] // bytes/server_ms 仅文档化响应格式，调度只用 node
struct SpeedtestData {
    /// 服务端自报节点名：planner 的同后端校验依据（所有节点入口必须指向同一后端）。
    #[serde(default)]
    node: String,
    #[serde(default)]
    bytes: u64,
    #[serde(default)]
    server_ms: i64,
}

/// 自适应路径：按大小定档（不等探测）→ describe → init → 多路传输（后台探测并发）→ complete。
async fn run_planned(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &PlannerSettings,
) -> Result<serde_json::Value, String> {
    let total = file
        .metadata()
        .map_err(|e| format!("读取文件信息失败: {}", e))?
        .len();
    let file_name = file
        .file_name()
        .ok_or_else(|| "无效路径".to_string())?
        .to_string_lossy()
        .to_string();

    // 节点池 + 持久化样本 + 主节点。探测只测无新鲜（<10min）样本的节点（planner 内部处理）。
    let pool = config.nodes.clone();
    if pool.is_empty() {
        // 节点池为空（normalize_nodes 正常情况下保证不为空）：退回默认分片的固定路径，保证能传。
        client.warn("upload", "节点池为空，退回固定分片上传".to_string());
        let desc = describe_async(file, DEFAULT_CHUNK_SIZE).await?;
        on_progress(0, desc.total_size);
        return run_once(client, file, remote_dir, &desc, options, on_progress)
            .await
            .map_err(|e| e.into_string());
    }
    let (main_node, state) = (config.main_node.clone(), config.state.clone());
    let input = planner::PlannerInput {
        total_size: total,
        main_node: main_node.clone(),
        now_ms: now_ms(),
        nodes: pool.clone(),
        state: state.clone(),
        adjust_cooldown_ms: planner::ADJUST_COOLDOWN_MS,
        // 探测恒定开启（快传与同步一样要占满多链路）；DRR 权重用平方根，多链路更均衡
        weight_exponent: planner::DEFAULT_WEIGHT_EXPONENT,
    };
    let mut p = planner::Planner::new(input).map_err(|e| format!("上传规划失败: {}", e))?;

    if let Some(size) = client.host().resume_chunk_size() {
        p.set_session_chunk_size(size)?;
    }

    // 分片大小只由文件大小决定，不再等探测：describe/init 立即开始，首字节即时发出。
    // 探测改为传输期后台并发补测（见下方统一循环），样本回来后经 report 并入调度。
    let chunk_size = match p.planned_chunk_size() {
        Some(c) => c as usize,
        None => return Err("上传规划失败：无法确定分片大小".to_string()),
    };
    log_plan_start(
        client,
        &pool,
        &state,
        &main_node,
        chunk_size as u64,
        total,
        options.quick_share,
    );
    // 传输期后台探测的样本聚合，结束时统一打一行规划日志。
    // 先用持久化的新鲜样本打底：本次跳过探测的节点也能在日志里给出 rtt/bw/后端。
    let now = now_ms();
    let mut probe_rtt: HashMap<String, u64> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS && n.rtt_ms > 0.0)
        .map(|n| (n.id.clone(), n.rtt_ms as u64))
        .collect();
    let mut probe_bps: HashMap<String, f64> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS && n.bps > 0.0)
        .map(|n| (n.id.clone(), n.bps))
        .collect();
    // 每节点探测返回的后端节点名：收尾日志用它判定"同后端校验"把谁踢出了上传池。
    let mut probe_backend: HashMap<String, String> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS && !n.backend_node.is_empty())
        .map(|n| (n.id.clone(), n.backend_node.clone()))
        .collect();
    // 每节点实际派出的分片数/字节：收尾日志里用它证明多链路到底有没有跑起来。
    let mut node_dispatch: HashMap<String, (u64, u64)> = HashMap::new();
    // 每节点成功/失败/慢片/均速实绩；与派发量分开，成功才算有效吞吐。
    let mut node_stats: HashMap<String, NodeStat> = HashMap::new();
    let mut probes_started: usize = 0;
    let mut probes_finished: usize = 0;

    let desc = describe_async(file, chunk_size).await?;
    on_progress(0, desc.total_size);

    let init = match call_init(
        client,
        &file_name,
        remote_dir,
        &desc,
        &options.device_id,
        &options.on_conflict,
        options.sync_base.as_deref(),
        options.quick_share,
    )
    .await
    {
        Ok(i) => i,
        Err(e) => {
            // init 失败也留下刚测的样本，下次（如换节点重试）10min 内免重复探测
            persist_planner_state(client, &p);
            return Err(e);
        }
    };

    // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
    // 不能调 complete（会 404 会话不存在），结果就地合成。
    if init.instant {
        if options.quick_share {
            return Err("快传收到意外的秒传响应".into());
        }
        on_progress(desc.total_size, desc.total_size);
        // 名字/路径以**服务端返回的**为准：同名冲突加了时间戳、或服务端另有落盘规则时，
        // 本地拼出来的是错的。缺字段才回退本地推断。
        let name = if init.file_name.is_empty() {
            file_name
        } else {
            init.file_name.clone()
        };
        let path = if init.storage_path.is_empty() {
            format!("{}/{}", remote_dir.trim_end_matches(['/', '\\']), name)
        } else {
            init.storage_path.clone()
        };
        persist_planner_state(client, &p);
        let data = UploadCompleteData {
            file_id: 0,
            file_name: name,
            storage_path: path,
            file_size: desc.total_size,
            file_hash: desc.file_hash_hex.clone(),
            synced: true,
        };
        return serde_json::to_value(&data).map_err(|e| format!("序列化上传结果失败: {}", e));
    }

    let missing: Vec<u32> = init.missing.iter().map(|&i| i as u32).collect();
    let bytes_sent = Arc::new(AtomicI64::new(
        (desc.chunk_count as i64 - missing.len() as i64) * desc.chunk_size,
    ));
    on_progress(
        bytes_sent.load(Ordering::Relaxed).min(desc.total_size),
        desc.total_size,
    );

    p.begin_transfer(&init.upload_id, &missing);
    let mut upload_id = init.upload_id.clone();
    log_initial_windows(client, &p);
    let transfer_started = Instant::now();

    // ── 统一传输循环：Chunk 与后台 Probe 都派 tokio task（channel 回报），Wait 挂 channel ──
    let (tx, mut rx) = mpsc::channel::<Outcome>(64);
    let mut tasks: JoinSet<()> = JoinSet::new();
    let mut chunk_handles = HashMap::new();
    let attempt_prefix = uuid::Uuid::new_v4().to_string();
    let mut win_snapshot: Vec<(String, usize)> = p.windows();
    let result: Result<serde_json::Value, String> = loop {
        match p.next_task() {
            planner::Task::Chunk(t) => {
                {
                    let e = node_dispatch.entry(t.node.clone()).or_insert((0, 0));
                    e.0 += 1;
                    e.1 += t.bytes;
                    node_stats.entry(t.node.clone()).or_default().dispatched += 1;
                }
                let tx = tx.clone();
                let client = client.clone();
                let path = file.to_path_buf();
                let uid = upload_id.clone();
                let desc_chunk = desc.chunk_size;
                let desc_total = desc.total_size;
                let seq = t.seq;
                let dispatched = t.clone();
                let attempt_id = format!("{attempt_prefix}-{seq}");
                let request_attempt = attempt_id.clone();
                let handle = tasks.spawn(async move {
                    let outcome = execute_chunk(
                        &client,
                        &path,
                        &uid,
                        desc_chunk,
                        desc_total,
                        &t,
                        &request_attempt,
                    )
                    .await;
                    let _ = tx.send(outcome).await;
                });
                chunk_handles.insert(seq, (dispatched, attempt_id, handle));
            }
            // 边传边测：探测与分片并发派发（planner 数据未传完就优先给探测），
            // 结果回来经 report 并入 DRR 池——多线路能否用起来全靠它。
            planner::Task::Probe(t) => {
                probes_started += 1;
                client.info(
                    "upload",
                    format!(
                        "[upload] 探测开始 node={} kind={} bytes={}",
                        t.node,
                        if t.kind == planner::ProbeKind::Tiny {
                            "rtt"
                        } else {
                            "bw"
                        },
                        t.bytes
                    ),
                );
                let tx = tx.clone();
                let client = client.clone();
                tasks.spawn(async move {
                    let outcome = run_probe(&client, &t).await;
                    let _ = tx.send(outcome).await;
                });
            }
            planner::Task::Wait => {
                // join_next 在 task 完成时也会就绪：正常结果走 channel，这里只兜 panic。
                // task 集为空时只剩 channel 能驱动，加停滞保护防挂死。
                let wake = if tasks.is_empty() {
                    tokio::select! {
                        o = rx.recv() => o.map(Wake::Outcome),
                        _ = tokio::time::sleep(STALL_TIMEOUT) => Some(Wake::Stalled),
                    }
                } else {
                    tokio::select! {
                        o = rx.recv() => o.map(Wake::Outcome),
                        j = tasks.join_next() => Some(Wake::TaskDone(j)),
                        _ = tokio::time::sleep(STALL_TIMEOUT) => Some(Wake::Stalled),
                    }
                };
                match wake {
                    Some(Wake::Outcome(Outcome::Report(r))) => {
                        if let planner::Report::ChunkUnavailable { seq } = &r {
                            if !p.is_chunk_inflight(*seq) {
                                continue;
                            }
                            let failed_node = &chunk_handles
                                .get(seq)
                                .expect("在途分片必须持有取消句柄")
                                .0
                                .node;
                            let mut attempts = Vec::new();
                            let mut urls = Vec::new();
                            for (task, attempt, handle) in chunk_handles.values() {
                                if &task.node == failed_node {
                                    handle.abort();
                                    attempts.push(serde_json::json!({"index": task.index, "attempt_id": attempt}));
                                }
                            }
                            // 入口均须属于同一后端；失败节点最后尝试，优先走已验证的其他入口。
                            for (node, window) in p.windows() {
                                if node != *failed_node && window > 0 {
                                    if let Some(cfg) = pool.iter().find(|cfg| cfg.id == node) {
                                        urls.push(cfg.url.clone());
                                    }
                                }
                            }
                            urls.push(chunk_handles.get(seq).unwrap().0.url.clone());
                            let body =
                                serde_json::json!({"upload_id": upload_id, "attempts": attempts});
                            let mut confirmed = false;
                            for url in urls {
                                match client
                                    .post_json_to::<serde_json::Value, _>(
                                        &url,
                                        "/file/upload/abandon",
                                        &body,
                                    )
                                    .await
                                {
                                    Ok(resp) if resp.is_ok() => {
                                        confirmed = true;
                                        break;
                                    }
                                    _ => {}
                                }
                            }
                            if !confirmed {
                                break Err(
                                    "无法确认后端已放弃旧分片尝试，停止上传以避免迟到写入".into()
                                );
                            }
                        }
                        record_outcome(&r, &chunk_handles, &mut node_stats);
                        if matches!(
                            &r,
                            planner::Report::ProbeOk { .. } | planner::Report::ProbeFailed { .. }
                        ) {
                            probes_finished += 1;
                        }
                        record_probe_sample(&r, &mut probe_rtt, &mut probe_bps, &mut probe_backend);
                        let reason = report_reason(&r);
                        if let planner::Report::ChunkOk { seq, bytes, .. } = &r {
                            if !p.is_chunk_inflight(*seq) {
                                continue;
                            }
                            let prev = bytes_sent.fetch_add(*bytes as i64, Ordering::Relaxed);
                            let now = (prev + *bytes as i64).min(desc.total_size);
                            on_progress(now, desc.total_size);
                        }
                        p.report(r);
                        chunk_handles.retain(|seq, (_, _, handle)| {
                            if p.is_chunk_inflight(*seq) {
                                return true;
                            }
                            handle.abort();
                            false
                        });
                        log_window_changes(client, &p, &mut win_snapshot, reason);
                    }
                    Some(Wake::Outcome(Outcome::Fatal(e))) => break Err(e),
                    // 分片 task 正常完成：结果经 channel 到，回到循环继续派
                    Some(Wake::TaskDone(Some(Ok(())))) => {}
                    Some(Wake::TaskDone(Some(Err(e)))) => {
                        if e.is_cancelled() {
                            continue;
                        }
                        break Err(format!("分片任务异常: {}", e));
                    }
                    // JoinSet 取空（正常不会发生，防御）：回循环继续
                    Some(Wake::TaskDone(None)) => {}
                    // 通道关闭（不可能：主 sender 还在）或停滞超时
                    None | Some(Wake::Stalled) => {
                        break Err("上传调度停滞：在途分片长时间无响应".into())
                    }
                }
            }
            planner::Task::Reinit => {
                // 旧会话的结果不能在 resume 后污染新会话进度或触发第二次 reinit。
                tasks.abort_all();
                chunk_handles.clear();
                while tasks.join_next().await.is_some() {}
                while rx.try_recv().is_ok() {}
                client.warn(
                    "upload",
                    format!("[upload] 会话过期，重新 init 后续传: {}", file_name),
                );
                let init2 = match call_init(
                    client,
                    &file_name,
                    remote_dir,
                    &desc,
                    &options.device_id,
                    &options.on_conflict,
                    options.sync_base.as_deref(),
                    options.quick_share,
                )
                .await
                {
                    Ok(i) => i,
                    Err(e) => break Err(e),
                };
                if init2.instant {
                    if options.quick_share {
                        break Err("快传收到意外的秒传响应".into());
                    }
                    // 重传期间已由其它设备传完同一内容：按完成处理（不能再走 complete）
                    on_progress(desc.total_size, desc.total_size);
                    let name = if init2.file_name.is_empty() {
                        file_name.clone()
                    } else {
                        init2.file_name.clone()
                    };
                    let path = if init2.storage_path.is_empty() {
                        format!("{}/{}", remote_dir.trim_end_matches(['/', '\\']), name)
                    } else {
                        init2.storage_path.clone()
                    };
                    let data = UploadCompleteData {
                        file_id: 0,
                        file_name: name,
                        storage_path: path,
                        file_size: desc.total_size,
                        file_hash: desc.file_hash_hex.clone(),
                        synced: true,
                    };
                    break serde_json::to_value(&data)
                        .map_err(|e| format!("序列化上传结果失败: {}", e));
                }
                upload_id = init2.upload_id.clone();
                let missing2: Vec<u32> = init2.missing.iter().map(|&i| i as u32).collect();
                // 进度基线按新的 missing 重算（服务端已收到的片不再重复计数）
                bytes_sent.store(
                    (desc.chunk_count as i64 - missing2.len() as i64) * desc.chunk_size,
                    Ordering::Relaxed,
                );
                on_progress(
                    bytes_sent.load(Ordering::Relaxed).min(desc.total_size),
                    desc.total_size,
                );
                p.resume(&upload_id, &missing2);
            }
            planner::Task::Done => match if options.quick_share {
                let eligible: Vec<String> = p
                    .windows()
                    .into_iter()
                    .filter(|(_, window)| *window > 0)
                    .filter_map(|(id, _)| {
                        pool.iter()
                            .find(|node| node.id == id)
                            .map(|node| node.url.clone())
                    })
                    .collect();
                complete_quick_share(client, &upload_id, &options.device_id, eligible).await
            } else {
                complete_upload(client, &upload_id, &options.device_id).await
            } {
                Ok(d) => break Ok(d),
                Err(e) => break Err(e),
            },
            planner::Task::Failed { reason } => break Err(reason),
        }
    };

    // 异常退出（Fatal/停滞）时在途分片/探测不再有意义的，全部放弃
    let probes_pending = probes_started.saturating_sub(probes_finished);
    if probes_pending > 0 {
        client.info(
            "upload",
            format!(
                "[upload] 探测收尾: 完成 {}/{}，{} 个未完成（随传输结束取消）",
                probes_finished, probes_started, probes_pending
            ),
        );
    }
    tasks.abort_all();
    // 计划日志放在传输结束后：此时才聚合齐后台探测样本（首字节早已发出，不受其阻塞）
    log_plan(
        client,
        &p,
        &pool,
        &probe_rtt,
        &probe_bps,
        &probe_backend,
        &main_node,
        chunk_size as u64,
        &node_dispatch,
    );
    let main_backend = probe_backend.get(&main_node).cloned().unwrap_or_default();
    log_transfer_summary(
        client,
        &pool,
        &p.windows(),
        &node_stats,
        &probe_rtt,
        &probe_bps,
        &probe_backend,
        &main_backend,
        transfer_started.elapsed(),
        options.quick_share,
    );
    persist_planner_state(client, &p);
    result
}

/// 聚合探测样本用于收尾规划日志（rtt / 实测带宽 / 后端节点名）。
fn record_probe_sample(
    r: &planner::Report,
    rtt: &mut HashMap<String, u64>,
    bps: &mut HashMap<String, f64>,
    backend: &mut HashMap<String, String>,
) {
    if let planner::Report::ProbeOk {
        node,
        kind,
        bytes,
        elapsed_ms,
        backend_node,
    } = r
    {
        if !backend_node.is_empty() {
            backend.insert(node.clone(), backend_node.clone());
        }
        match kind {
            planner::ProbeKind::Tiny => {
                rtt.insert(node.clone(), *elapsed_ms);
            }
            planner::ProbeKind::Throughput => {
                let secs = *elapsed_ms as f64 / 1000.0;
                if secs > 0.0 {
                    bps.insert(node.clone(), *bytes as f64 / secs);
                }
            }
        }
    }
}

/// 每节点传输实绩：派发量、成功量、失败量、慢片数和累计传输耗时。
/// 收尾日志用它给出「每个节点到底跑了多少、均速多少」，而不是只看规划时的窗口。
#[derive(Default)]
struct NodeStat {
    dispatched: u64,
    ok: u64,
    failed: u64,
    slow: u64,
    bytes: u64,
    active_ms: u64,
    last_error: String,
}

/// 把一次分片结果记到对应节点上。分片回报只带 seq，节点要从派发句柄表反查。
fn record_outcome(
    r: &planner::Report,
    handles: &HashMap<u64, (planner::ChunkTask, String, tokio::task::AbortHandle)>,
    stats: &mut HashMap<String, NodeStat>,
) {
    let seq = match r {
        planner::Report::ChunkOk { seq, .. }
        | planner::Report::ChunkCongested { seq }
        | planner::Report::ChunkUnavailable { seq }
        | planner::Report::ChunkDataError { seq }
        | planner::Report::ChunkSessionGone { seq } => *seq,
        _ => return,
    };
    let Some((task, _, _)) = handles.get(&seq) else {
        return;
    };
    let s = stats.entry(task.node.clone()).or_default();
    match r {
        planner::Report::ChunkOk {
            bytes, elapsed_ms, ..
        } => {
            s.ok += 1;
            s.bytes += *bytes;
            s.active_ms += *elapsed_ms;
            if *elapsed_ms > 5_000 {
                s.slow += 1;
            }
        }
        _ => {
            s.failed += 1;
            s.last_error = report_reason(r).to_string();
        }
    }
}

/// 传输结束后的实绩汇总：整体吞吐、每节点成功/失败/慢片/均速、最终离线或未参与的节点。
fn log_transfer_summary(
    client: &Client,
    pool: &[planner::PlannerNodeConfig],
    windows: &[(String, usize)],
    stats: &HashMap<String, NodeStat>,
    probe_rtt: &HashMap<String, u64>,
    probe_bps: &HashMap<String, f64>,
    probe_backend: &HashMap<String, String>,
    main_backend: &str,
    elapsed: Duration,
    quick: bool,
) {
    let secs = elapsed.as_secs_f64().max(0.001);
    let sent: u64 = stats.values().map(|s| s.bytes).sum();
    let ok_chunks: u64 = stats.values().map(|s| s.ok).sum();
    let failed: u64 = stats.values().map(|s| s.failed).sum();
    let used = stats.values().filter(|s| s.ok > 0).count();
    client.info(
        "upload",
        format!(
            "[upload] 汇总: 模式={} 传输耗时={:.1}s 实传={} 均速={} 成功={}片 失败={} 使用节点={}/{}",
            if quick { "快传" } else { "同步" },
            secs,
            fmt_bytes(sent),
            fmt_bps(sent as f64 / secs),
            ok_chunks,
            failed,
            used,
            pool.len()
        ),
    );
    let mut offline: Vec<String> = Vec::new();
    // 未参与的真实原因：后端不一致 > 已测未入池 > 吞吐探测未完成 > 未探测。
    let reason_for = |id: &str| -> String {
        if let Some(nb) = probe_backend.get(id) {
            if !main_backend.is_empty() && nb != main_backend {
                format!("后端不一致:{}", nb)
            } else if probe_bps.contains_key(id) {
                "已测未入池".to_string()
            } else {
                "吞吐探测未完成".to_string()
            }
        } else if probe_rtt.contains_key(id) {
            "吞吐探测未完成".to_string()
        } else {
            "未探测".to_string()
        }
    };
    for n in pool {
        let w = windows
            .iter()
            .find(|(id, _)| id == &n.id)
            .map(|(_, w)| *w)
            .unwrap_or(0);
        let Some(s) = stats.get(&n.id) else {
            if w == 0 {
                offline.push(format!("{}({})", n.id, reason_for(&n.id)));
            }
            continue;
        };
        let speed = if s.active_ms > 0 {
            fmt_bps(s.bytes as f64 / (s.active_ms as f64 / 1000.0))
        } else {
            "-".to_string()
        };
        client.info(
            "upload",
            format!(
                "[upload] 节点实绩 node={} 窗口={} 派发={} 成功={}片/{} 失败={} 慢片={} 均速={}{}",
                n.id,
                w,
                s.dispatched,
                s.ok,
                fmt_bytes(s.bytes),
                s.failed,
                s.slow,
                speed,
                if s.last_error.is_empty() {
                    String::new()
                } else {
                    format!(" 最后错误={}", s.last_error)
                }
            ),
        );
        if w == 0 {
            offline.push(format!(
                "{}({})",
                n.id,
                if s.failed > 0 {
                    "失败".to_string()
                } else {
                    reason_for(&n.id)
                }
            ));
        }
    }
    if !offline.is_empty() {
        client.warn(
            "upload",
            format!("[upload] 最终离线/未参与节点: {}", offline.join(", ")),
        );
    }
}

enum Wake {
    Outcome(Outcome),
    TaskDone(Option<Result<(), tokio::task::JoinError>>),
    Stalled,
}

/// 执行一个探测任务：POST planner 指定节点的测速接口，把响应当 RTT/带宽样本回报。
/// 探测结果逐条打日志——多线路能否用起来，全看这里的成败，不要静默吞掉失败原因。
async fn run_probe(client: &Client, t: &planner::ProbeTask) -> Outcome {
    let kind = if t.kind == planner::ProbeKind::Tiny {
        "rtt"
    } else {
        "bw"
    };
    let body = vec![0u8; t.bytes as usize];
    let bytes_q = t.bytes.to_string();
    let params = [("bytes", bytes_q.as_str())];
    let started = Instant::now();
    let resp: Result<Response<SpeedtestData>, String> = client
        .post_bytes_to(
            &t.url,
            "/net/speedtest/upload",
            &params,
            body,
            Some(Duration::from_secs(30)),
        )
        .await;
    match resp {
        Err(e) if client.host().is_cancelled_error(&e) => Outcome::Fatal(e),
        Err(e) => {
            client.warn(
                "upload",
                format!("[upload] 探测失败 node={} kind={}: {}", t.node, kind, e),
            );
            Outcome::Report(planner::Report::ProbeFailed {
                node: t.node.clone(),
                kind: t.kind,
            })
        }
        Ok(r) => {
            if r.is_ok() {
                let data = r.data.unwrap_or_default();
                let elapsed_ms = started.elapsed().as_millis().max(1) as u64;
                if t.kind == planner::ProbeKind::Throughput {
                    let bps = t.bytes as f64 / (elapsed_ms as f64 / 1000.0);
                    client.info(
                        "upload",
                        format!(
                            "[upload] 探测 node={} bw={}/s ({}ms) backend={}",
                            t.node,
                            fmt_bps(bps),
                            elapsed_ms,
                            if data.node.is_empty() {
                                "?"
                            } else {
                                &data.node
                            }
                        ),
                    );
                } else {
                    client.info(
                        "upload",
                        format!(
                            "[upload] 探测 node={} rtt={}ms backend={}",
                            t.node,
                            elapsed_ms,
                            if data.node.is_empty() {
                                "?"
                            } else {
                                &data.node
                            }
                        ),
                    );
                }
                Outcome::Report(planner::Report::ProbeOk {
                    node: t.node.clone(),
                    kind: t.kind,
                    bytes: t.bytes,
                    elapsed_ms,
                    backend_node: data.node,
                })
            } else {
                client.warn(
                    "upload",
                    format!(
                        "[upload] 探测失败 node={} kind={}: [{}] {}",
                        t.node, kind, r.code, r.message
                    ),
                );
                Outcome::Report(planner::Report::ProbeFailed {
                    node: t.node.clone(),
                    kind: t.kind,
                })
            }
        }
    }
}

/// 执行一个分片任务：定位读出字节 → POST 到 planner 指定的节点 URL → 分类成 Report。
/// 结果分类：2xx→ChunkOk（bytes 为实际字节数，供进度与 planner 的 EMA 用）；
/// 422→ChunkDataError；404→ChunkSessionGone；10秒期限/断链→ChunkUnavailable。
/// 平台级失败（切节点作废/读文件失败）→ Fatal。
async fn execute_chunk(
    client: &Client,
    file: &Path,
    upload_id: &str,
    chunk_size: i64,
    total_size: i64,
    t: &planner::ChunkTask,
    attempt_id: &str,
) -> Outcome {
    let data = match crate::file::read_chunk(file, t.index as i32, chunk_size as usize, total_size)
    {
        Ok(data) => data,
        Err(e) => return Outcome::Fatal(format!("读取分片失败: {e}")),
    };
    let len = data.len();
    let started = Instant::now();
    let resp = tokio::time::timeout(
        CHUNK_FAILOVER_TIMEOUT,
        client.upload_chunk(
            Some(&t.url),
            upload_id,
            t.index as i32,
            data,
            Some(Duration::from_millis(t.timeout_ms)),
            attempt_id,
        ),
    )
    .await;
    let resp = match resp {
        Ok(resp) => resp,
        Err(_) => {
            client.warn(
                "upload",
                format!(
                    "[upload] node={} chunk={} 超过10秒未完成（{}B），取消该节点在途分片并切换链路",
                    t.node, t.index, len
                ),
            );
            return Outcome::Report(planner::Report::ChunkUnavailable { seq: t.seq });
        }
    };
    let elapsed_ms = started.elapsed().as_millis().max(1) as u64;
    match resp {
        Err(e) if client.host().is_cancelled_error(&e) => Outcome::Fatal(e),
        Err(e)
            if e.starts_with("HTTP 502")
                || e.starts_with("HTTP 503")
                || e.starts_with("HTTP 504")
                || (!e.starts_with("HTTP ") && !e.starts_with("JSON 解析失败")) =>
        {
            client.warn(
                "upload",
                format!(
                    "[upload] node={} chunk={} 链路失败 {}ms {}B，切换节点: {}",
                    t.node, t.index, elapsed_ms, len, e
                ),
            );
            Outcome::Report(planner::Report::ChunkUnavailable { seq: t.seq })
        }
        // HTTP/解析错误：保留原有预算重试，不直接判定节点断链。
        Err(e) => {
            client.warn(
                "upload",
                format!(
                    "[upload] node={} chunk={} HTTP/解析错误 {}ms {}B，按拥塞处理: {}",
                    t.node, t.index, elapsed_ms, len, e
                ),
            );
            Outcome::Report(planner::Report::ChunkCongested { seq: t.seq })
        }
        Ok(resp) => {
            if resp.is_ok() {
                if elapsed_ms > 5_000 {
                    client.warn(
                        "upload",
                        format!(
                            "[upload] node={} chunk={} 慢片 {}ms {}B（>5s，可能链路拥塞）",
                            t.node, t.index, elapsed_ms, len
                        ),
                    );
                }
                Outcome::Report(planner::Report::ChunkOk {
                    seq: t.seq,
                    bytes: len as u64,
                    elapsed_ms,
                })
            } else {
                match resp.code {
                    422 => Outcome::Report(planner::Report::ChunkDataError { seq: t.seq }),
                    404 => Outcome::Report(planner::Report::ChunkSessionGone { seq: t.seq }),
                    502 | 503 | 504 => {
                        Outcome::Report(planner::Report::ChunkUnavailable { seq: t.seq })
                    }
                    // 其它业务码：HTTP 链路是通的，按拥塞处理占掉一次派发预算
                    _ => {
                        client.warn(
                            "upload",
                            format!(
                                "[upload] node={} chunk={} 业务码={} 按拥塞处理: {}",
                                t.node, t.index, resp.code, resp.message
                            ),
                        );
                        Outcome::Report(planner::Report::ChunkCongested { seq: t.seq })
                    }
                }
            }
        }
    }
}

/// 固定路径（run_once 用）：读出该片字节并上传（单次尝试，业务码 422/404 直接返回）。
/// 返回该片字节数（用于累计进度）。分片级重试已由 planner 的 CHUNK_ATTEMPTS 取代。
async fn read_and_upload_chunk(
    client: &Client,
    file: &Path,
    upload_id: &str,
    index: i32,
    chunk_size: usize,
    total_size: i64,
) -> Result<usize, UploadError> {
    let data = crate::file::read_chunk(file, index, chunk_size, total_size)
        .map_err(|e| UploadError::Other(format!("读取分片失败: {e}")))?;
    let len = data.len();
    let resp = client
        .upload_chunk(None, upload_id, index, data, None, "")
        .await
        .map_err(UploadError::Other)?;
    if resp.is_ok() {
        return Ok(len);
    }
    // 业务码区分（信封 code，HTTP 恒 200）
    match resp.code {
        404 => Err(UploadError::SessionGone),
        422 => Err(UploadError::Other(format!(
            "分片 {} 校验失败: {}",
            index, resp.message
        ))),
        _ => Err(UploadError::Other(format!(
            "分片 {} 失败: {}",
            index, resp.message
        ))),
    }
}

async fn run_once(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    desc: &Description,
    options: &UploadOptions,
    on_progress: ProgressFn,
) -> Result<serde_json::Value, UploadError> {
    let file_name = file
        .file_name()
        .ok_or_else(|| UploadError::Other("无效路径".into()))?
        .to_string_lossy()
        .to_string();

    let init = call_init(
        client,
        &file_name,
        remote_dir,
        desc,
        &options.device_id,
        &options.on_conflict,
        options.sync_base.as_deref(),
        options.quick_share,
    )
    .await
    .map_err(UploadError::Other)?;

    // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
    // 不能调 complete（会 404 会话不存在），结果就地合成。
    if init.instant {
        if options.quick_share {
            return Err(UploadError::Other("快传收到意外的秒传响应".into()));
        }
        on_progress(desc.total_size, desc.total_size);
        let name = if init.file_name.is_empty() {
            file_name
        } else {
            init.file_name.clone()
        };
        let path = if init.storage_path.is_empty() {
            format!("{}/{}", remote_dir.trim_end_matches(['/', '\\']), name)
        } else {
            init.storage_path.clone()
        };
        let data = UploadCompleteData {
            file_id: 0,
            file_name: name,
            storage_path: path,
            file_size: desc.total_size,
            file_hash: desc.file_hash_hex.clone(),
            synced: true,
        };
        return serde_json::to_value(&data)
            .map_err(|e| UploadError::Other(format!("序列化上传结果失败: {}", e)));
    }

    let missing = init.missing.clone();
    let bytes_sent = Arc::new(AtomicI64::new(
        (desc.chunk_count as i64 - missing.len() as i64) * desc.chunk_size,
    ));
    on_progress(
        bytes_sent.load(Ordering::Relaxed).min(desc.total_size),
        desc.total_size,
    );

    use tokio::sync::Semaphore;
    let sem = Arc::new(Semaphore::new(
        options.concurrency.unwrap_or(DEFAULT_CONCURRENCY).max(1),
    ));
    let mut handles = JoinSet::new();
    for index in missing {
        let permit = sem.clone();
        let upload_id = init.upload_id.clone();
        let chunk_size = desc.chunk_size as usize;
        let total_size = desc.total_size;
        let path = file.to_path_buf();
        let bs = bytes_sent.clone();
        let op = on_progress.clone();
        // Client 内部 reqwest::Client 是 Arc，clone 便宜，可 move 进 spawn
        let client_clone = client.clone();
        handles.spawn(async move {
            let _p = permit
                .acquire_owned()
                .await
                .map_err(|e| UploadError::Other(e.to_string()))?;
            // 让 cancel 信号有机会插入
            tokio::task::yield_now().await;
            let len = read_and_upload_chunk(
                &client_clone,
                &path,
                &upload_id,
                index,
                chunk_size,
                total_size,
            )
            .await?;
            let prev = bs.fetch_add(len as i64, Ordering::Relaxed);
            let now = (prev + len as i64).min(total_size);
            op(now, total_size);
            Ok(())
        });
    }

    while let Some(result) = handles.join_next().await {
        match result {
            Ok(Ok(())) => {}
            Ok(Err(e)) => return Err(e),
            Err(e) => return Err(UploadError::Other(format!("分片任务异常: {}", e))),
        }
    }

    complete_upload(client, &init.upload_id, &options.device_id)
        .await
        .map_err(UploadError::Other)
}

async fn call_init(
    client: &Client,
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
    sync_base: Option<&str>,
    quick_share: bool,
) -> Result<UploadInitData, String> {
    let params = UploadInitParams {
        // 快传模式下服务端自算目标路径，path 传空即可（普通模式仍须为目标目录）
        path: if quick_share {
            String::new()
        } else {
            remote_dir.to_string()
        },
        name: name.to_string(),
        total_size: desc.total_size,
        chunk_size: desc.chunk_size,
        chunk_count: desc.chunk_count,
        merkle_root: desc.merkle_root_hex.clone(),
        file_hash: desc.file_hash_hex.clone(),
        leaf_hashes: desc.leaf_hashes_hex.clone(),
        device_id: device_id.to_string(),
        on_conflict: on_conflict.to_string(),
        sync: sync_base.is_some(),
        base_hash: sync_base.unwrap_or("").to_string(),
        quick_share,
    };
    let resp = client.post("/file/upload/init", &params).await?;
    if resp.is_ok() {
        let data: UploadInitData = resp.data.ok_or("init 响应为空")?;
        if !data.instant {
            if data.upload_id.is_empty()
                || (data.chunk_size > 0 && data.chunk_size != desc.chunk_size)
                || (data.chunk_count > 0 && data.chunk_count != desc.chunk_count)
            {
                return Err("init 会话参数与文件描述不一致".into());
            }
            let mut seen = std::collections::HashSet::new();
            if data
                .missing
                .iter()
                .any(|&i| i < 0 || i >= desc.chunk_count || !seen.insert(i))
            {
                return Err("init 缺失分片列表无效".into());
            }
        }
        Ok(data)
    } else if resp.code == 409 {
        Err(format!("{}: {}", SYNC_CONFLICT, resp.message))
    } else {
        Err(format!("init 失败: {}", resp.message))
    }
}

async fn complete_upload(
    client: &Client,
    upload_id: &str,
    device_id: &str,
) -> Result<serde_json::Value, String> {
    let params = UploadCompleteParams {
        upload_id: upload_id.to_string(),
        device_id: device_id.to_string(),
    };
    // data 原样返回（serde_json::Value），由 upload()/quick_share_upload() 按模式解析：
    // 普通上传是文件记录，快传是分享记录——服务端保证两者各自形状稳定。
    let resp: Response<serde_json::Value> = client.post("/file/upload/complete", &params).await?;
    if resp.is_ok() {
        resp.data.ok_or_else(|| "complete 响应为空".into())
    } else {
        Err(format!("complete 失败: {}", resp.message))
    }
}

/// 收尾请求也可跨同后端入口重试。后端保留完成结果，响应丢失不会生成第二个分享或404。
async fn complete_quick_share(
    client: &Client,
    upload_id: &str,
    device_id: &str,
    mut candidates: Vec<String>,
) -> Result<serde_json::Value, String> {
    candidates.sort_by_key(|url| !url.starts_with("https://"));
    let mut seen = std::collections::HashSet::new();
    candidates.retain(|url| seen.insert(url.clone()));
    if candidates.is_empty() {
        return Err("没有同后端的可用上传收尾节点".into());
    }
    let params = UploadCompleteParams {
        upload_id: upload_id.into(),
        device_id: device_id.into(),
    };
    let mut last_error = String::new();
    let deadline = Instant::now() + Duration::from_secs(180);
    while Instant::now() < deadline {
        for base in &candidates {
            match client
                .post_json_to_timeout::<serde_json::Value, _>(
                    base,
                    "/file/upload/complete",
                    &params,
                    Duration::from_secs(10),
                )
                .await
            {
                Ok(resp) if resp.is_ok() => {
                    let mut data = resp.data.ok_or("complete 响应为空")?;
                    data["entry_url"] = serde_json::json!(base.trim_end_matches('/'));
                    data["entry_candidates"] = serde_json::json!(candidates);
                    return Ok(data);
                }
                Ok(resp) if matches!(resp.code, 401 | 403 | 404 | 422) => {
                    return Err(format!("complete 失败: {}", resp.message))
                }
                Ok(resp) => last_error = resp.message,
                Err(error) => last_error = error,
            }
        }
        tokio::time::sleep(Duration::from_millis(300)).await;
    }
    Err(format!("上传收尾节点均失败: {last_error}"))
}

/// 传输开始前的规划日志：把真正交给 planner 的节点池、主节点、探测开关、分片档位、
/// 以及哪些节点有新鲜持久化样本（因此会跳过现场探测）一次性打出来。
/// 这是判断「到底配了几条链路 / 为什么看起来只有一条」的第一手依据。
fn log_plan_start(
    client: &Client,
    pool: &[planner::PlannerNodeConfig],
    state: &planner::PersistedState,
    main_node: &str,
    chunk_size: u64,
    total: u64,
    quick: bool,
) {
    let now = now_ms();
    let fresh_ids: Vec<String> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS)
        .map(|n| n.id.clone())
        .collect();
    let pool_desc: Vec<String> = pool
        .iter()
        .map(|n| match &n.parent {
            Some(p) => format!("{}({}←{})", n.id, n.url, p),
            None => format!("{}({})", n.id, n.url),
        })
        .collect();
    client.info(
        "upload",
        format!(
            "[upload] 规划: 模式={} 主节点={} 分片={} 大小={} 节点池[{}] 新鲜样本[{}]",
            if quick { "快传" } else { "同步" },
            main_node,
            fmt_chunk(chunk_size),
            fmt_bytes(total),
            if pool_desc.is_empty() {
                "-".to_string()
            } else {
                pool_desc.join(", ")
            },
            if fresh_ids.is_empty() {
                "-".to_string()
            } else {
                fresh_ids.join(",")
            },
        ),
    );
    if pool.len() < 2 {
        client.warn(
            "upload",
            format!(
                "[upload] 节点池只有 {} 个节点，本次上传为单链路（多路径需 ≥2 个节点）",
                pool.len()
            ),
        );
    }
}

/// 开始传输时的初始窗口：conc=0 表示该节点此刻还不在上传池里（等探测结果或本就在池外）。
fn log_initial_windows(client: &Client, p: &planner::Planner) {
    let w: Vec<String> = p
        .windows()
        .into_iter()
        .map(|(id, n)| format!("{}={}", id, n))
        .collect();
    client.info("upload", format!("[upload] 初始窗口: {}", w.join(" ")));
}

/// 收尾规划日志：每节点 rtt/bw/eff/最终窗口 + 实际派发量 + 出池原因，chunk=分片档位。
/// 示例：[upload] node=ddns rtt=20ms bw=1.3MB/s eff=1.3MB/s conc=2 [在用] 实传=42片/168MB
fn log_plan(
    client: &Client,
    p: &planner::Planner,
    pool: &[planner::PlannerNodeConfig],
    probe_rtt: &HashMap<String, u64>,
    probe_bps: &HashMap<String, f64>,
    probe_backend: &HashMap<String, String>,
    main_node: &str,
    chunk_size: u64,
    node_dispatch: &HashMap<String, (u64, u64)>,
) {
    let measured: HashMap<String, f64> = probe_bps.clone();
    let eff = planner::combine_effective(pool, &measured);
    let windows = p.windows();
    let main_backend = probe_backend.get(main_node).cloned().unwrap_or_default();
    client.info(
        "upload",
        format!("[upload] 规划收尾: chunk={}", fmt_chunk(chunk_size)),
    );
    for n in pool {
        let conc = windows
            .iter()
            .find(|(id, _)| id == &n.id)
            .map(|(_, w)| *w)
            .unwrap_or(0);
        let rtt = probe_rtt
            .get(&n.id)
            .map(|v| format!("{}ms", v))
            .unwrap_or_else(|| "-".to_string());
        let b = probe_bps
            .get(&n.id)
            .map(|v| fmt_bps(*v))
            .unwrap_or_else(|| "-".into());
        let e = eff
            .get(&n.id)
            .map(|v| fmt_bps(*v))
            .unwrap_or_else(|| "-".into());
        let (cnt, bytes) = node_dispatch.get(&n.id).copied().unwrap_or((0, 0));
        // 出池原因：同后端校验不符 / 仅测到 RTT 吞吐失败 / 探测失败或未探测
        let state = if conc > 0 {
            "在用".to_string()
        } else if let Some(nb) = probe_backend.get(&n.id) {
            if !main_backend.is_empty() && nb != &main_backend {
                format!("出池:后端不一致(本节点={} 主节点={})", nb, main_backend)
            } else if probe_bps.contains_key(&n.id) {
                "出池:已测未入池".to_string()
            } else {
                "出池:吞吐探测失败/未完成".to_string()
            }
        } else if probe_rtt.contains_key(&n.id) {
            "出池:吞吐探测失败".to_string()
        } else {
            "出池:探测失败/未探测".to_string()
        };
        client.info(
            "upload",
            format!(
                "[upload] node={} rtt={} bw={} eff={} conc={} [{}] 实传={}片/{}",
                n.id,
                rtt,
                b,
                e,
                conc,
                state,
                cnt,
                fmt_bytes(bytes)
            ),
        );
    }
    let used = node_dispatch.values().filter(|(c, _)| *c > 0).count();
    if used >= 2 {
        client.info(
            "upload",
            format!("[upload] 多链路生效: {} 个节点参与上传", used),
        );
    } else {
        client.warn(
            "upload",
            format!(
                "[upload] 单链路：仅 {} 个节点参与上传（其余节点见上方出池原因）",
                used
            ),
        );
    }
}

/// 窗口变化日志：[upload] node=jp window 8→4 (timeout)
fn log_window_changes(
    client: &Client,
    p: &planner::Planner,
    snapshot: &mut Vec<(String, usize)>,
    reason: &str,
) {
    let now = p.windows();
    for (id, w) in &now {
        if let Some((_, old)) = snapshot.iter().find(|(sid, _)| sid == id) {
            if old != w {
                let (kind, desc) = match (*old, *w) {
                    (0, n) if n > 0 => ("入池", format!("0→{}", n)),
                    (n, 0) if n > 0 => ("出池", format!("{}→0", n)),
                    _ => ("窗口", format!("{}→{}", old, w)),
                };
                client.info(
                    "upload",
                    format!("[upload] node={} {} {} ({})", id, kind, desc, reason),
                );
            }
        }
    }
    *snapshot = now;
}

/// 窗口变化原因：跟随最近一次回报类型（halving 由 timeout/data 触发，加窗由连续成功 streak 触发）。
fn report_reason(r: &planner::Report) -> &'static str {
    match r {
        planner::Report::ChunkOk { .. } => "streak",
        planner::Report::ChunkCongested { .. } => "congested",
        planner::Report::ChunkUnavailable { .. } => "unavailable",
        planner::Report::ChunkDataError { .. } => "data",
        planner::Report::ChunkSessionGone { .. } => "gone",
        _ => "probe",
    }
}

/// 把 planner 导出的测速样本写回 config.yml：下次上传 10min 内直接复用，不重复探测。
fn persist_planner_state(client: &Client, p: &planner::Planner) {
    client.host().save_planner_state(p.export_state());
}

/// bps → 人类可读（1.3MB/s / 500KB/s / 80B/s）。
fn fmt_bps(bps: f64) -> String {
    if bps >= 1_000_000.0 {
        format!("{:.1}MB/s", bps / 1_000_000.0)
    } else if bps >= 1_000.0 {
        format!("{:.0}KB/s", bps / 1_000.0)
    } else {
        format!("{:.0}B/s", bps)
    }
}

/// 字节数 → 档位可读（8MB / 512KB / 64KB）。
fn fmt_chunk(n: u64) -> String {
    if n >= (1 << 20) && n % (1 << 20) == 0 {
        format!("{}MB", n >> 20)
    } else if n >= (1 << 10) && n % (1 << 10) == 0 {
        format!("{}KB", n >> 10)
    } else {
        format!("{}B", n)
    }
}

/// 任意字节数 → 人类可读（1.5GB / 320MB / 4.0MB / 512KB / 800B），带一位小数。
fn fmt_bytes(n: u64) -> String {
    let f = n as f64;
    if f >= 1e9 {
        format!("{:.1}GB", f / 1e9)
    } else if f >= 1e6 {
        format!("{:.1}MB", f / 1e6)
    } else if f >= 1e3 {
        format!("{:.0}KB", f / 1e3)
    } else {
        format!("{}B", n)
    }
}

fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod execution_tests {
    use super::*;
    struct Hanging;
    impl UploadHost for Hanging {
        fn request(&self, _: UploadRequest) -> HostFuture<'_, Response<serde_json::Value>> {
            Box::pin(std::future::pending())
        }
        fn server_url(&self) -> &str {
            "http://unit.invalid"
        }
        fn for_upload_session(&self) -> Arc<dyn UploadHost> {
            Arc::new(Self)
        }
        fn probe_share_url<'a>(&'a self, _: &'a str) -> HostFuture<'a, ()> {
            Box::pin(std::future::pending())
        }
    }
    #[tokio::test(start_paused = true)]
    async fn response_body_stall_is_abandoned_after_ten_seconds() {
        let file = std::env::temp_dir().join(format!("sys-core-stall-{}", uuid::Uuid::new_v4()));
        std::fs::write(&file, b"chunk").unwrap();
        let client = Client::new(Arc::new(Hanging));
        let task = planner::ChunkTask {
            seq: 7,
            node: "stalled".into(),
            url: "http://unit.invalid".into(),
            index: 0,
            timeout_ms: 120_000,
            bytes: 5,
        };
        let started = tokio::time::Instant::now();
        let result = execute_chunk(&client, &file, "session", 5, 5, &task, "attempt-7").await;
        std::fs::remove_file(file).unwrap();
        assert!(matches!(
            result,
            Outcome::Report(planner::Report::ChunkUnavailable { seq: 7 })
        ));
        assert_eq!(started.elapsed(), CHUNK_FAILOVER_TIMEOUT);
    }
}
