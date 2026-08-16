// chunked_uploader.rs
// 分片上传编排：算描述信息（叶子/树根/整文件 blake3）→ init →（秒传则完成）
//               → 乱序并发补传缺失分片 → complete。
//
// 设计要点：
// - 并发：后端 chunk 接口支持乱序/可并发，本端用 Semaphore 限流的协程池并发上传，大幅提速大文件。
// - 进度：字节级，基于已成功分片累计字节数回调；init 阶段先置「已落盘分片」估算基线。
// - 取消：本函数是 async，调用方 cancel 它对应的 task 即停止派发新分片；已在途分片随 reqwest 抛错。
//   每个分片任务开头 `yield_now().await` 让 cancel 信号尽快传播。
// - 会话过期：任一分片收 code==404 → 视作 SessionGone，由外层 upload() 捕获后自动重新 init 整流程一次
//   （不会重算哈希；二次仍失败则原样返回 Err）。
//
// 哈希规则与 file_lib/README.md 一致：叶子=blake3(分片字节)；parent=blake3(left‖right)；
// 奇数节点原样进位；空→blake3("")；单叶子→该叶子本身。与 Android Blake3Util.kt 逐字节一致。
use crate::api::client::ApiClient;
use crate::api::file::{api as file_api, params::*, response::*};
use blake3::Hasher;
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use tokio::sync::Semaphore;

/// 默认分片 4MiB。
pub const DEFAULT_CHUNK_SIZE: usize = 4 * 1024 * 1024;

/// 默认并发分片数。
pub const DEFAULT_CONCURRENCY: usize = 3;

/// 单片校验失败时的重传次数。
const CHUNK_RETRY: usize = 3;

/// 进度回调类型：`(已发送字节, 总字节)`。Arc 以便多 task 共享。
pub type ProgressFn = Arc<dyn Fn(i64, i64) + Send + Sync + 'static>;

#[derive(Clone)]
pub struct UploadOptions {
    pub chunk_size: usize,
    pub concurrency: usize,
    /// 本机设备 id，服务端派发同步任务时排除源设备。
    pub device_id: Arc<str>,
    /// 目标同名时的策略：空/"reject" = 报错（默认，同步链路必须用这个），
    /// "timestamp" = 服务端自动给文件名加时间戳区分（发布 APK 这类同名是常态的场景）。
    pub on_conflict: Arc<str>,
}

impl UploadOptions {
    pub fn new(device_id: impl Into<String>) -> Self {
        UploadOptions {
            chunk_size: DEFAULT_CHUNK_SIZE,
            concurrency: DEFAULT_CONCURRENCY,
            device_id: Arc::from(device_id.into().into_boxed_str()),
            on_conflict: Arc::from(""),
        }
    }

    /// 同名自动加时间戳（不影响其它字段）。
    pub fn with_timestamp_on_conflict(mut self) -> Self {
        self.on_conflict = Arc::from("timestamp");
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
/// 空 → blake3("")；单叶子 → 该叶子本身。规则同 file_lib/src/lib.rs 的 merkle_root。
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
pub async fn upload(
    client: &ApiClient,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
) -> Result<UploadCompleteData, String> {
    let desc =
        describe(file, options.chunk_size).map_err(|e| format!("计算文件描述信息失败: {}", e))?;
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
    )
    .await
    .map_err(UploadError::Other)?;

    // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
    // 不能调 complete（会 404 会话不存在），结果就地合成。
    if init.instant {
        on_progress(desc.total_size, desc.total_size);
        // 名字/路径以**服务端返回的**为准：同名冲突加了时间戳、或服务端另有落盘规则时，
        // 本地拼出来的是错的（曾经这里直接把 remote_dir 当 storage_path 返回，
        // 发布 APK 时会把「目录」当成 file_path 登记上去）。缺字段才回退本地推断。
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

    let sem = Arc::new(Semaphore::new(options.concurrency.max(1)));
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

/// 读出该片字节并上传（含分片级重试，业务码 422 视作 ChunkVerify 重试，404 抛 SessionGone）。
/// 返回该片字节数（用于累计进度）。
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
    use std::os::windows::fs::FileExt;
    let f = std::fs::File::open(file).map_err(|e| UploadError::Other(e.to_string()))?;
    f.seek_read(&mut data, offset as u64)
        .map_err(|e| UploadError::Other(format!("定位读失败: {}", e)))?;
    drop(f);

    let mut last_err: Option<String> = None;
    for _ in 0..CHUNK_RETRY {
        let resp = file_api::upload_chunk(client, upload_id, index, data.clone())
            .await
            .map_err(UploadError::Other)?;
        if resp.is_ok() {
            return Ok(len);
        }
        // 业务码区分
        match resp.code {
            404 => return Err(UploadError::SessionGone),
            422 => {
                // 分片校验失败：可能网络抖动，相同字节重传有机会
                last_err = Some(resp.message);
                continue;
            }
            _ => {
                return Err(UploadError::Other(format!(
                    "分片 {} 失败: {}",
                    index, resp.message
                )))
            }
        }
    }
    Err(UploadError::Other(format!(
        "分片 {} 校验失败: {}",
        index,
        last_err.unwrap_or_else(|| "未知".into())
    )))
}

async fn call_init(
    client: &ApiClient,
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
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
    };
    let resp = file_api::upload_init(client, params).await?;
    if resp.is_ok() {
        resp.data.ok_or_else(|| "init 响应为空".into())
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
