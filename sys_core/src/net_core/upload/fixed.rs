use super::session::{call_init, complete_upload};
use super::{Client, ProgressFn, UploadOptions, DEFAULT_CONCURRENCY};
use crate::file_core::file::{read_chunk, Description};
use protocol::UploadCompleteData;
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use tokio::task::JoinSet;

use super::protocol;

pub(super) enum UploadError {
    Other(String),
    SessionGone,
}

impl UploadError {
    pub(super) fn is_session_gone(&self) -> bool {
        matches!(self, UploadError::SessionGone)
    }

    pub(super) fn into_string(self) -> String {
        match self {
            UploadError::Other(s) => s,
            UploadError::SessionGone => "会话已过期".into(),
        }
    }
}

/// 固定路径：读出该片字节并单次上传；业务码 422/404 直接返回，不做恢复重试。
async fn read_and_upload_chunk(
    client: &Client,
    file: &Path,
    upload_id: &str,
    index: i32,
    chunk_size: usize,
    total_size: i64,
) -> Result<usize, UploadError> {
    let data = read_chunk(file, index, chunk_size, total_size)
        .map_err(|e| UploadError::Other(format!("读取分片失败: {e}")))?;
    let len = data.len();
    let resp = client
        .upload_chunk(None, upload_id, index, data, None, "")
        .await
        .map_err(UploadError::Other)?;
    if resp.is_ok() {
        return Ok(len);
    }
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

pub(super) async fn run_once(
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

    // 秒传没有建会话，不能调 complete；文件名和路径以服务端返回值为准。
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
        let client_clone = client.clone();
        handles.spawn(async move {
            let _p = permit
                .acquire_owned()
                .await
                .map_err(|e| UploadError::Other(e.to_string()))?;
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
