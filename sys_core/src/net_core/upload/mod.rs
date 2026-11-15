// 分片上传编排：计算文件描述（叶子/树根/整文件 blake3）→ init →（秒传则完成）
// → 乱序并发补传缺失分片 → complete。
//
// 两条路径：
// - 自适应（默认）：分片大小只由文件大小决定，describe→init 立即开始；传输期由 planner
//   做多路径调度，后台探测与分片并发。暂时性断网时按 planner 的 Recover 等待，或由平台
//   对会话请求做短间隔重试；等待均随上传任务取消而中止。
// - 固定覆盖（UploadOptions.chunk_size 有值）：跳过 planner，init/chunk 全走激活节点的旧
//   单路径行为，可预期。
//
// planner 是同步对象，planned 是它的平台侧：分片与探测各派一个 tokio task，结果经 channel
// 喂回主循环；Task::Wait 等在途结果，Task::Recover 只睡眠指定时长，不触发停滞超时。
// Chunk 任务带明确 node url，必须打到该 URL（多路径必须落在同一会话上，由 planner 做
// 同后端校验）。
//
// 进度：字节级，基于已成功分片累计字节数回调；init 阶段先置「已落盘分片」估算基线。
// 取消：调用方取消 upload future 即停止等待与派发；已在途分片随之取消。
// 会话过期：分片收 code==404 → 回报 ChunkSessionGone → planner 发 Task::Reinit → 本端用
// 同一 Description 重新 init（不重算哈希）→ planner.resume 续传。
//
// 哈希规则与 sync_core/README.md 一致：叶子=blake3(分片字节)；parent=blake3(left‖right)；
// 奇数节点原样进位；空→blake3("")；单叶子→该叶子本身。各平台通过 JNI/NAPI 复用同一实现。
pub mod bridge;
mod bridge_ffi;
mod executor;
mod fixed;
mod host;
mod logging;
mod planned;
pub mod protocol;
mod session;

pub use crate::file_core::file::file_blake3_hex;
use crate::file_core::file::{describe, Description};
pub use bridge_ffi::{
    fc_upload_body_free, fc_upload_bridge_version, fc_upload_cancel, fc_upload_poll,
    fc_upload_reply, fc_upload_start, fc_upload_take_body,
};
use fixed::run_once;
pub use host::{
    Client, HostFuture, PlannerSettings, RequestBody, Response, UploadHost, UploadRequest,
};
use planned::run_planned;
use protocol::*;
use std::path::Path;
use std::sync::Arc;

/// 默认分片 4MiB（固定覆盖路径的缺省值）。
pub const DEFAULT_CHUNK_SIZE: usize = 4 * 1024 * 1024;

/// 默认并发分片数（固定覆盖路径的缺省值）。
pub const DEFAULT_CONCURRENCY: usize = 3;

/// 进度回调类型：`(已发送字节, 总字节)`。Arc 以便多 task 共享。
pub type ProgressFn = Arc<dyn Fn(i64, i64) + Send + Sync + 'static>;

/// 大文件描述计算不占异步运行时线程；暂停/取消命令能够立即被处理。
pub(super) async fn describe_async(file: &Path, chunk_size: usize) -> Result<Description, String> {
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

/// upload() / quick_share_upload() 共用的传输主线：describe → init → 传片 → complete，
/// 返回 complete 响应的原始 JSON，由调用方按模式解析。
async fn upload_inner(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &PlannerSettings,
) -> Result<serde_json::Value, String> {
    if let Some(chunk_size) = options.chunk_size {
        // 固定覆盖路径：完全走旧单路径行为（含 SessionGone 重 init 一次），不探测、不多路径、
        // 不启用自适应路径的暂时错误恢复循环。
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
            Err(e) if e.is_session_gone() => run_once(
                client,
                file,
                remote_dir,
                &desc,
                options,
                on_progress.clone(),
            )
            .await
            .map_err(|e2| e2.into_string()),
            Err(e) => Err(e.into_string()),
        }
    } else {
        run_planned(client, file, remote_dir, options, on_progress, config).await
    }
}
