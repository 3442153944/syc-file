// chunked_uploader.rs
// 分片上传编排：算描述信息（叶子/树根/整文件 blake3）→ init →（秒传则完成）
//               → 乱序并发补传缺失分片 → complete。
//
// 两条路径：
// - 自适应（默认）：先跑 sync_core::upload_planner 的探测循环（每节点微样本测 RTT + 吞吐
//   样本测带宽，10min 内有新鲜持久化样本则跳过），按总带宽选分片档位；传输期由 planner
//   做多路径调度（父链折算共享瓶颈、per-node AIMD 窗口、动态超时、重排重试 CHUNK_ATTEMPTS=3
//   全部由 planner 负责，本端不再做分片级内部重试）。
// - 固定覆盖（UploadOptions.chunk_size 有值）：跳过探测，init/chunk 全走激活节点的旧
//   单路径行为，可预期。
//
// planner 是同步对象，本模块是它的平台侧：探测 inline await；分片每片一个 tokio task，
// 结果经 channel 喂回本循环；Task::Wait = 无任务可分派，挂在 channel 上等在途分片的 Report。
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
use crate::api::client::{ApiClient, ApiResponse, NODE_SWITCHED};
use crate::api::file::{api as file_api, params::*, response::*};
use crate::api::routes;
use crate::config::SharedSyncConfig;
use crate::logger;
use blake3::Hasher;
use std::collections::HashMap;
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use sync_core::upload_planner as planner;
use tokio::sync::mpsc;
use tokio::task::JoinSet;

/// 默认分片 4MiB（固定覆盖路径的缺省值）。
pub const DEFAULT_CHUNK_SIZE: usize = 4 * 1024 * 1024;

/// 默认并发分片数（固定覆盖路径的缺省值）。
pub const DEFAULT_CONCURRENCY: usize = 3;

/// 传输循环在 channel 与在途分片都无进展时的停滞保护：planner 的单片超时上限 120s，
/// 超过 2 倍仍无任何分片完成说明调度或执行挂死，报错退出而不是干等。
const STALL_TIMEOUT: Duration = Duration::from_secs(240);

/// 进度回调类型：`(已发送字节, 总字节)`。Arc 以便多 task 共享。
pub type ProgressFn = Arc<dyn Fn(i64, i64) + Send + Sync + 'static>;

#[derive(Clone)]
pub struct UploadOptions {
    /// 分片大小覆盖：None=自适应（planner 探测选档 + 多路径调度，默认）；
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
        }
    }

    /// 标记为同步链路的覆盖式上传，并带上本机已知的基线 hash（没有基线传空串）。
    pub fn with_sync_base(mut self, base_hash: impl AsRef<str>) -> Self {
        self.sync_base = Some(Arc::from(base_hash.as_ref()));
        self
    }

    /// 同名自动加时间戳（不影响其它字段）。
    pub fn with_timestamp_on_conflict(mut self) -> Self {
        self.on_conflict = Arc::from("timestamp");
        self
    }

    /// 显式固定分片大小与并发数，跳过 planner 的自适应探测（保留旧单路径行为的开关）。
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

/// 一趟顺序读文件：算每片 blake3 叶子 + 整文件流式 blake3，再由叶子构 Merkle 树根。
fn describe(file: &Path, chunk_size: usize) -> std::io::Result<Description> {
    let total = file.metadata()?.len() as i64;
    let count = if total == 0 {
        1
    } else {
        ((total as usize) + chunk_size - 1) / chunk_size
    };
    let mut leaves: Vec<[u8; 32]> = Vec::with_capacity(count);
    let mut leaf_hex: Vec<String> = Vec::with_capacity(count);
    let mut file_hasher = Hasher::new();

    use std::io::{BufReader, Read};
    let f = std::fs::File::open(file)?;
    let mut input = BufReader::new(f);
    let mut buf = vec![0u8; chunk_size];
    loop {
        let mut filled = 0;
        while filled < buf.len() {
            let n = input.read(&mut buf[filled..])?;
            if n == 0 {
                break;
            }
            filled += n;
        }
        if filled == 0 {
            break;
        }
        let block = &buf[..filled];
        file_hasher.update(block);
        let leaf = *blake3::hash(block).as_bytes();
        leaves.push(leaf);
        leaf_hex.push(hex::encode(leaf));
        if filled < buf.len() {
            break; // 末片
        }
    }

    let root = merkle_root(&leaves);
    let file_hash = file_hasher.finalize();
    Ok(Description {
        total_size: total,
        chunk_size: chunk_size as i64,
        chunk_count: leaves.len() as i32,
        leaf_hashes_hex: leaf_hex,
        merkle_root_hex: hex::encode(root),
        file_hash_hex: file_hash.to_hex().to_string(),
    })
}

/// Merkle 树根：叶子两两合并 parent = blake3(left ‖ right)，奇数节点原样进位到上层。
/// 空 → blake3("")；单叶子 → 该叶子本身。规则同 sync_core/src/lib.rs 的 merkle_root。
fn merkle_root(leaves: &[[u8; 32]]) -> [u8; 32] {
    if leaves.is_empty() {
        return *blake3::hash(&[]).as_bytes();
    }
    let mut level: Vec<[u8; 32]> = leaves.to_vec();
    while level.len() > 1 {
        let mut next: Vec<[u8; 32]> = Vec::with_capacity((level.len() + 1) / 2);
        let mut i = 0;
        while i < level.len() {
            if i + 1 < level.len() {
                let mut h = Hasher::new();
                h.update(&level[i]);
                h.update(&level[i + 1]);
                next.push(*h.finalize().as_bytes());
                i += 2;
            } else {
                next.push(level[i]); // 奇数节点进位
                i += 1;
            }
        }
        level = next;
    }
    level[0]
}

struct Description {
    total_size: i64,
    chunk_size: i64,
    chunk_count: i32,
    leaf_hashes_hex: Vec<String>,
    merkle_root_hex: String,
    file_hash_hex: String,
}

/// 上传整份文件。
/// `on_progress(sent_bytes, total_bytes)` 在 init 后及每片成功后回调。
/// `config` 提供节点池 / 激活节点 / planner 持久化状态（自适应路径用；固定路径不读）。
pub async fn upload(
    client: &ApiClient,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &SharedSyncConfig,
) -> Result<UploadCompleteData, String> {
    if let Some(chunk_size) = options.chunk_size {
        // 固定覆盖路径：完全走旧单路径行为（describe→init→激活节点并发补传→complete，
        // 含 SessionGone 重 init 一次），不探测、不多路径。
        let desc =
            describe(file, chunk_size).map_err(|e| format!("计算文件描述信息失败: {}", e))?;
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
        // 自适应路径：planner 探测选档 + 多路径调度
        run_planned(client, file, remote_dir, options, on_progress, config).await
    }
}

/// 计算文件的 blake3 hex（流式读取，不一次性入内存）。供下载校验等场景复用。
pub fn file_blake3_hex(file: &Path) -> std::io::Result<String> {
    use std::io::{BufReader, Read};
    let f = std::fs::File::open(file)?;
    let mut input = BufReader::new(f);
    let mut hasher = Hasher::new();
    let mut buf = vec![0u8; 64 * 1024];
    loop {
        let n = input.read(&mut buf)?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(hasher.finalize().to_hex().to_string())
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

/// 自适应路径：planner 探测 → describe（用选出的档位）→ init → 多路径传输 → complete。
async fn run_planned(
    client: &ApiClient,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &SharedSyncConfig,
) -> Result<UploadCompleteData, String> {
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
    let pool: Vec<planner::PlannerNodeConfig> = {
        let cfg = config.read();
        cfg.nodes
            .iter()
            .map(|n| planner::PlannerNodeConfig {
                id: n.id.clone(),
                url: n.server_url.clone(),
                parent: n.parent.clone(),
            })
            .collect()
    };
    if pool.is_empty() {
        // 节点池为空（normalize_nodes 正常情况下保证不为空）：退回默认分片的固定路径，保证能传。
        logger::warn("upload", "节点池为空，退回固定分片上传".to_string());
        let desc = describe(file, DEFAULT_CHUNK_SIZE)
            .map_err(|e| format!("计算文件描述信息失败: {}", e))?;
        on_progress(0, desc.total_size);
        return run_once(client, file, remote_dir, &desc, options, on_progress)
            .await
            .map_err(|e| e.into_string());
    }
    let input = {
        let cfg = config.read();
        let state = cfg
            .upload_planner_state
            .as_ref()
            .and_then(|v| serde_json::from_value::<planner::PersistedState>(v.clone()).ok())
            .unwrap_or_default();
        planner::PlannerInput {
            total_size: total,
            main_node: cfg.active_node_id.clone(),
            now_ms: now_ms(),
            nodes: pool.clone(),
            state,
            adjust_cooldown_ms: planner::ADJUST_COOLDOWN_MS,
        }
    };
    let mut p = planner::Planner::new(input).map_err(|e| format!("上传规划失败: {}", e))?;

    // ── 探测循环：planner 一次只派一个探测，inline 执行回结果后再拿下一个 ──
    // 聚合每个节点的 rtt/bps 样本， finalize 后统一打一行探测日志。
    let mut probe_rtt: HashMap<String, u64> = HashMap::new();
    let mut probe_bps: HashMap<String, f64> = HashMap::new();
    loop {
        match p.next_task() {
            planner::Task::Probe(t) => {
                let node = t.node.clone();
                match run_probe(client, &t).await {
                    Outcome::Report(r) => {
                        if let planner::Report::ProbeOk {
                            kind,
                            bytes,
                            elapsed_ms,
                            ..
                        } = &r
                        {
                            match kind {
                                planner::ProbeKind::Tiny => {
                                    probe_rtt.insert(node.clone(), *elapsed_ms);
                                }
                                planner::ProbeKind::Throughput => {
                                    let secs = *elapsed_ms as f64 / 1000.0;
                                    if secs > 0.0 {
                                        probe_bps
                                            .insert(node.clone(), *bytes as f64 / secs);
                                    }
                                }
                            }
                        }
                        p.report(r);
                    }
                    Outcome::Fatal(e) => return Err(e),
                }
            }
            // 探测 inline 执行，不会有未决探测：Wait = 队列探完、规划完成
            planner::Task::Wait => break,
            other => return Err(format!("上传规划异常任务: {:?}", other)),
        }
    }
    let chunk_size = match p.planned_chunk_size() {
        Some(c) => c as usize,
        None => {
            return Err(p
                .fail_reason()
                .unwrap_or("上传规划失败：没有可用路径")
                .to_string())
        }
    };
    log_plan(&p, &pool, &probe_rtt, &probe_bps, chunk_size as u64);

    // describe 依赖分片档位，探测选档完成后再算哈希（一趟顺序读，叶子+树根+整文件）。
    let desc = describe(file, chunk_size).map_err(|e| format!("计算文件描述信息失败: {}", e))?;
    on_progress(0, desc.total_size);

    let init = match call_init(
        client,
        &file_name,
        remote_dir,
        &desc,
        &options.device_id,
        &options.on_conflict,
        options.sync_base.as_deref(),
    )
    .await
    {
        Ok(i) => i,
        Err(e) => {
            // init 失败也留下刚测的样本，下次（如换节点重试）10min 内免重复探测
            persist_planner_state(config, &p);
            return Err(e);
        }
    };

    // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
    // 不能调 complete（会 404 会话不存在），结果就地合成。
    if init.instant {
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
        persist_planner_state(config, &p);
        return Ok(UploadCompleteData {
            file_id: 0,
            file_name: name,
            storage_path: path,
            file_size: desc.total_size,
            file_hash: desc.file_hash_hex.clone(),
            synced: true,
        });
    }

    let missing: Vec<u32> = if !init.missing.is_empty() {
        init.missing.iter().map(|&i| i as u32).collect()
    } else {
        (0..desc.chunk_count).map(|i| i as u32).collect()
    };
    let bytes_sent = Arc::new(AtomicI64::new(
        (desc.chunk_count as i64 - missing.len() as i64) * desc.chunk_size,
    ));
    on_progress(
        bytes_sent.load(Ordering::Relaxed).min(desc.total_size),
        desc.total_size,
    );

    p.begin_transfer(&init.upload_id, &missing);
    let mut upload_id = init.upload_id.clone();

    // ── 传输循环：Chunk 派 tokio task（channel 回报），Wait 挂在 channel 上 ──
    let (tx, mut rx) = mpsc::channel::<Outcome>(64);
    let mut tasks: JoinSet<()> = JoinSet::new();
    let mut win_snapshot: Vec<(String, usize)> = p.windows();
    let result: Result<UploadCompleteData, String> = loop {
        match p.next_task() {
            planner::Task::Chunk(t) => {
                let tx = tx.clone();
                let client = client.clone();
                let path = file.to_path_buf();
                let uid = upload_id.clone();
                let desc_chunk = desc.chunk_size;
                let desc_total = desc.total_size;
                tasks.spawn(async move {
                    let outcome = execute_chunk(&client, &path, &uid, desc_chunk, desc_total, &t).await;
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
                    }
                };
                match wake {
                    Some(Wake::Outcome(Outcome::Report(r))) => {
                        let reason = report_reason(&r);
                        if let planner::Report::ChunkOk { bytes, .. } = &r {
                            let prev = bytes_sent.fetch_add(*bytes as i64, Ordering::Relaxed);
                            let now = (prev + *bytes as i64).min(desc.total_size);
                            on_progress(now, desc.total_size);
                        }
                        p.report(r);
                        log_window_changes(&p, &mut win_snapshot, reason);
                    }
                    Some(Wake::Outcome(Outcome::Fatal(e))) => break Err(e),
                    // 分片 task 正常完成：结果经 channel 到，回到循环继续派
                    Some(Wake::TaskDone(Some(Ok(())))) => {}
                    Some(Wake::TaskDone(Some(Err(e)))) => {
                        break Err(format!("分片任务异常: {}", e))
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
                logger::warn(
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
                )
                .await
                {
                    Ok(i) => i,
                    Err(e) => break Err(e),
                };
                if init2.instant {
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
                    break Ok(UploadCompleteData {
                        file_id: 0,
                        file_name: name,
                        storage_path: path,
                        file_size: desc.total_size,
                        file_hash: desc.file_hash_hex.clone(),
                        synced: true,
                    });
                }
                upload_id = init2.upload_id.clone();
                let missing2: Vec<u32> = if !init2.missing.is_empty() {
                    init2.missing.iter().map(|&i| i as u32).collect()
                } else {
                    (0..desc.chunk_count).map(|i| i as u32).collect()
                };
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
            planner::Task::Done => match complete_upload(client, &upload_id, &options.device_id).await
            {
                Ok(d) => break Ok(d),
                Err(e) => break Err(e),
            },
            planner::Task::Failed { reason } => break Err(reason),
            planner::Task::Probe(_) => break Err("上传规划异常：传输阶段出现探测任务".into()),
        }
    };

    // 异常退出（Fatal/停滞）时在途分片不再有意义的，全部放弃
    tasks.abort_all();
    persist_planner_state(config, &p);
    result
}

enum Wake {
    Outcome(Outcome),
    TaskDone(Option<Result<(), tokio::task::JoinError>>),
    Stalled,
}

/// 执行一个探测任务：POST planner 指定节点的测速接口，把响应当 RTT/带宽样本回报。
async fn run_probe(client: &ApiClient, t: &planner::ProbeTask) -> Outcome {
    let body = vec![0u8; t.bytes as usize];
    let bytes_q = t.bytes.to_string();
    let params = [("bytes", bytes_q.as_str())];
    let started = Instant::now();
    let resp: Result<ApiResponse<SpeedtestData>, String> = client
        .post_bytes_to(
            &t.url,
            routes::NET_SPEEDTEST_UPLOAD,
            &params,
            body,
            Some(Duration::from_secs(30)),
        )
        .await;
    match resp {
        Err(e) if e.contains(NODE_SWITCHED) => Outcome::Fatal(e),
        Err(_) => Outcome::Report(planner::Report::ProbeFailed {
            node: t.node.clone(),
            kind: t.kind,
        }),
        Ok(r) => {
            if r.is_ok() {
                let data = r.data.unwrap_or_default();
                Outcome::Report(planner::Report::ProbeOk {
                    node: t.node.clone(),
                    kind: t.kind,
                    bytes: t.bytes,
                    elapsed_ms: started.elapsed().as_millis().max(1) as u64,
                    backend_node: data.node,
                })
            } else {
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
/// 422→ChunkDataError；404→ChunkSessionGone；超时/传输错误→ChunkCongested。
/// 平台级失败（切节点作废/读文件失败）→ Fatal。
async fn execute_chunk(
    client: &ApiClient,
    file: &Path,
    upload_id: &str,
    chunk_size: i64,
    total_size: i64,
    t: &planner::ChunkTask,
) -> Outcome {
    let offset = (t.index as i64) * chunk_size;
    let len = if total_size == 0 {
        0
    } else {
        total_size - offset
    };
    let len = std::cmp::min(chunk_size, len);
    if len < 0 {
        return Outcome::Fatal(format!(
            "分片 {} 越界 (offset={}, total={})",
            t.index, offset, total_size
        ));
    }
    let mut data = vec![0u8; len as usize];
    let f = match std::fs::File::open(file) {
        Ok(f) => f,
        Err(e) => return Outcome::Fatal(format!("打开文件失败: {}", e)),
    };
    #[cfg(windows)]
    {
        use std::os::windows::fs::FileExt;
        if let Err(e) = f.seek_read(&mut data, offset as u64) {
            return Outcome::Fatal(format!("定位读失败: {}", e));
        }
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::FileExt;
        if let Err(e) = f.read_at(&mut data, offset as u64) {
            return Outcome::Fatal(format!("定位读失败: {}", e));
        }
    }
    drop(f);

    let started = Instant::now();
    let resp = file_api::upload_chunk_to(
        client,
        &t.url,
        upload_id,
        t.index as i32,
        data,
        t.timeout_ms,
    )
    .await;
    let elapsed_ms = started.elapsed().as_millis().max(1) as u64;
    match resp {
        Err(e) if e.contains(NODE_SWITCHED) => Outcome::Fatal(e),
        // 超时/传输层错误 = 拥塞信号，交给 planner 减半窗口并重排
        Err(_) => Outcome::Report(planner::Report::ChunkCongested { seq: t.seq }),
        Ok(resp) => {
            if resp.is_ok() {
                Outcome::Report(planner::Report::ChunkOk {
                    seq: t.seq,
                    bytes: len as u64,
                    elapsed_ms,
                })
            } else {
                match resp.code {
                    422 => Outcome::Report(planner::Report::ChunkDataError { seq: t.seq }),
                    404 => Outcome::Report(planner::Report::ChunkSessionGone { seq: t.seq }),
                    // 其它业务码：HTTP 链路是通的，按拥塞处理占掉一次派发预算
                    _ => Outcome::Report(planner::Report::ChunkCongested { seq: t.seq }),
                }
            }
        }
    }
}

/// 固定路径（run_once 用）：读出该片字节并上传（单次尝试，业务码 422/404 直接返回）。
/// 返回该片字节数（用于累计进度）。分片级重试已由 planner 的 CHUNK_ATTEMPTS 取代。
async fn read_and_upload_chunk(
    client: &ApiClient,
    file: &Path,
    upload_id: &str,
    index: i32,
    chunk_size: usize,
    total_size: i64,
) -> Result<usize, UploadError> {
    let offset = (index as i64) * (chunk_size as i64);
    let len = std::cmp::min(chunk_size as i64, total_size - offset) as usize;
    let mut data = vec![0u8; len];
    let f = std::fs::File::open(file).map_err(|e| UploadError::Other(e.to_string()))?;
    #[cfg(windows)]
    {
        use std::os::windows::fs::FileExt;
        f.seek_read(&mut data, offset as u64)
            .map_err(|e| UploadError::Other(format!("定位读失败: {}", e)))?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::FileExt;
        f.read_at(&mut data, offset as u64)
            .map_err(|e| UploadError::Other(format!("定位读失败: {}", e)))?;
    }
    drop(f);

    let resp = file_api::upload_chunk(client, upload_id, index, data)
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
    client: &ApiClient,
    file: &Path,
    remote_dir: &str,
    desc: &Description,
    options: &UploadOptions,
    on_progress: ProgressFn,
) -> Result<UploadCompleteData, UploadError> {
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
    )
    .await
    .map_err(UploadError::Other)?;

    // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
    // 不能调 complete（会 404 会话不存在），结果就地合成。
    if init.instant {
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
        return Ok(UploadCompleteData {
            file_id: 0,
            file_name: name,
            storage_path: path,
            file_size: desc.total_size,
            file_hash: desc.file_hash_hex.clone(),
            synced: true,
        });
    }

    let missing: Vec<i32> = if !init.missing.is_empty() {
        init.missing.clone()
    } else {
        (0..desc.chunk_count).collect()
    };
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
    let mut handles = Vec::with_capacity(missing.len());
    for index in missing {
        let permit = sem.clone();
        let upload_id = init.upload_id.clone();
        let chunk_size = desc.chunk_size as usize;
        let total_size = desc.total_size;
        let path = file.to_path_buf();
        let bs = bytes_sent.clone();
        let op = on_progress.clone();
        // ApiClient 内部 reqwest::Client 是 Arc，clone 便宜，可 move 进 spawn
        let client_clone = client.clone();
        let h: tokio::task::JoinHandle<Result<(), UploadError>> = tokio::spawn(async move {
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
        handles.push(h);
    }

    for h in handles {
        match h.await {
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
    client: &ApiClient,
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
    sync_base: Option<&str>,
) -> Result<UploadInitData, String> {
    let params = UploadInitParams {
        path: remote_dir.to_string(),
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
    };
    let resp = file_api::upload_init(client, params).await?;
    if resp.is_ok() {
        resp.data.ok_or_else(|| "init 响应为空".into())
    } else if resp.code == 409 {
        Err(format!("{}: {}", SYNC_CONFLICT, resp.message))
    } else {
        Err(format!("init 失败: {}", resp.message))
    }
}

async fn complete_upload(
    client: &ApiClient,
    upload_id: &str,
    device_id: &str,
) -> Result<UploadCompleteData, String> {
    let params = UploadCompleteParams {
        upload_id: upload_id.to_string(),
        device_id: device_id.to_string(),
    };
    let resp = file_api::upload_complete(client, params).await?;
    if resp.is_ok() {
        resp.data.ok_or_else(|| "complete 响应为空".into())
    } else {
        Err(format!("complete 失败: {}", resp.message))
    }
}

/// 探测完成后打一行规划日志：每节点 rtt=微样本 b=实测带宽 eff=父链折算后带宽
/// chunk=选出的档位 conc=初始窗口。示例：[upload] probe node=ddns rtt=20ms b=1.3MB/s eff=1.3MB/s chunk=8MB conc=1
fn log_plan(
    p: &planner::Planner,
    pool: &[planner::PlannerNodeConfig],
    probe_rtt: &HashMap<String, u64>,
    probe_bps: &HashMap<String, f64>,
    chunk_size: u64,
) {
    let measured: HashMap<String, f64> = probe_bps.clone();
    let eff = planner::combine_effective(pool, &measured);
    let windows = p.windows();
    for n in pool {
        let (Some(&rtt), Some(&b)) = (probe_rtt.get(&n.id), probe_bps.get(&n.id)) else {
            continue;
        };
        let e = eff.get(&n.id).copied().unwrap_or(b);
        let conc = windows
            .iter()
            .find(|(id, _)| id == &n.id)
            .map(|(_, w)| *w)
            .unwrap_or(0);
        logger::info(
            "upload",
            format!(
                "[upload] probe node={} rtt={}ms b={}/s eff={}/s chunk={} conc={}",
                n.id,
                rtt,
                fmt_bps(b),
                fmt_bps(e),
                fmt_chunk(chunk_size),
                conc
            ),
        );
    }
}

/// 窗口变化日志：[upload] node=jp window 8→4 (timeout)
fn log_window_changes(
    p: &planner::Planner,
    snapshot: &mut Vec<(String, usize)>,
    reason: &str,
) {
    let now = p.windows();
    for (id, w) in &now {
        if let Some((_, old)) = snapshot.iter().find(|(sid, _)| sid == id) {
            if old != w {
                logger::info(
                    "upload",
                    format!("[upload] node={} window {}→{} ({})", id, old, w, reason),
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
        planner::Report::ChunkCongested { .. } => "timeout",
        planner::Report::ChunkDataError { .. } => "data",
        planner::Report::ChunkSessionGone { .. } => "gone",
        _ => "probe",
    }
}

/// 把 planner 导出的测速样本写回 config.yml：下次上传 10min 内直接复用，不重复探测。
fn persist_planner_state(config: &SharedSyncConfig, p: &planner::Planner) {
    match serde_json::to_value(p.export_state()) {
        Ok(v) => {
            let mut cfg = config.write();
            cfg.upload_planner_state = Some(v);
            cfg.save();
        }
        Err(e) => logger::warn("upload", format!("planner 状态序列化失败: {}", e)),
    }
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

fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}
