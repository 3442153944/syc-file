// upload_worker.rs
// 职责：同步场景的文件上传 worker 池。
// 只做调度：从 channel 取任务 → 删除旧文件（sync overwrite）→ 分片上传 → 上报 file_changed。
// 哈希统一用 blake3（与 chunked_uploader / file_lib 一致），base_store 存 blake3 hex。
use crate::api::{
    client::ApiClient,
    file::api as file_api,
    file::params::DeleteFileParams,
    sync::{api as sync_api, params::NotifyParams},
};
use crate::chunked_uploader::{self, UploadOptions};
use crate::config::SharedSyncConfig;
use serde::Serialize;
use std::path::PathBuf;
use std::sync::Arc;
use tauri::{AppHandle, Emitter};
use tokio::sync::mpsc;

/// 一次上传任务
#[derive(Debug)]
pub struct UploadTask {
    pub local_path: PathBuf,
    pub remote_dir: String,
    pub folder_id: u64,
    pub relative_path: String,
    /// create / modify（空串回退 "modify" 兼容旧调用方）
    pub action: String,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UploadProgress {
    pub path: String,
    pub status: String,
    pub error: Option<String>,
}

pub fn start_upload_workers(
    worker_count: usize,
    config: SharedSyncConfig,
    app: AppHandle,
) -> mpsc::Sender<UploadTask> {
    let (tx, rx) = mpsc::channel::<UploadTask>(256);
    let rx = std::sync::Arc::new(tokio::sync::Mutex::new(rx));

    for _ in 0..worker_count {
        let rx = rx.clone();
        let config = config.clone();
        let app = app.clone();
        tokio::spawn(async move {
            loop {
                let task = { rx.lock().await.recv().await };
                match task {
                    None => break,
                    Some(t) => upload_file(t, &config, &app).await,
                }
            }
        });
    }
    tx
}

async fn upload_file(task: UploadTask, config: &SharedSyncConfig, app: &AppHandle) {
    let path_str = task.local_path.to_string_lossy().to_string();

    let (client, device_id) = {
        let cfg = config.read();
        if cfg.server_url.is_empty() || cfg.token.is_empty() {
            return;
        }
        (ApiClient::new(&cfg.server_url, &cfg.token), cfg.device_id.clone())
    };

    let file_name = match task.local_path.file_name() {
        Some(n) => n.to_string_lossy().to_string(),
        None => return,
    };

    // 回声抑制：当前内容与基线一致 → 多半是本机刚从服务端下载/发布的文件被
    // watcher 抓到，原样回传只会让服务端再派发一圈（乒乓循环）。直接跳过。
    if let Some(base) = crate::base_store::get(task.folder_id, &task.relative_path) {
        if let Ok(cur) = chunked_uploader::file_blake3_hex(&task.local_path) {
            if cur == base.hash {
                crate::logger::debug("upload", format!("内容与基线一致，跳过回传: {}", task.relative_path));
                return;
            }
        }
    }

    emit_progress(app, &path_str, "uploading", None);

    // 同步场景需要覆盖已存在文件：先删远端旧文件（不存在则忽略 404），再分片上传
    let _ = file_api::delete_file(
        &client,
        DeleteFileParams {
            path: task.remote_dir.clone(),
            name: file_name.clone(),
        },
    )
    .await;

    // 分片上传（blake3 + merkle + 乱序并发 + 断点续传 + 秒传 + SessionGone 重试一次）
    let options = UploadOptions::new(device_id.clone());
    let on_progress: chunked_uploader::ProgressFn = Arc::new(|_, _| {});

    let complete = match chunked_uploader::upload(
        &client,
        &task.local_path,
        &task.remote_dir,
        &options,
        on_progress,
    )
    .await
    {
        Ok(d) => d,
        Err(e) => {
            crate::logger::error("upload", format!("上传失败 {}: {}", path_str, e));
            emit_progress(app, &path_str, "error", Some(e));
            return;
        }
    };

    let file_hash = complete.file_hash.clone();
    let file_size = complete.file_size;

    // 上报 file_changed（带 base_hash 供服务端 CAS）
    let mtime = task
        .local_path
        .metadata()
        .ok()
        .and_then(|m| m.modified().ok())
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_secs() as i64);

    let folder_id = task.folder_id;
    let rel = task.relative_path.clone();
    let base_hash = crate::base_store::get(folder_id, &rel).map(|b| b.hash);
    let action = if task.action.is_empty() { "modify" } else { &task.action };

    sync_api::notify(
        &client,
        NotifyParams {
            device_id,
            folder_id,
            relative_path: rel.clone(),
            file_name,
            action: action.into(),
            file_size: Some(file_size),
            file_hash: Some(file_hash.clone()),
            base_hash,
            is_dir: false,
            mtime,
        },
    )
    .await
    .ok();

    // 上传被接受后，trunk hash 即为本次内容；更新基线（若实际冲突，conflict 处理会再纠正）。
    crate::base_store::set_with_file(folder_id, &rel, &file_hash, &task.local_path);
    crate::logger::info("upload", format!("已上传并上报: {} ({} bytes)", rel, file_size));

    emit_progress(app, &path_str, "done", None);
}

fn emit_progress(app: &AppHandle, path: &str, status: &str, error: Option<String>) {
    app.emit(
        "upload-progress",
        UploadProgress {
            path: path.to_string(),
            status: status.to_string(),
            error,
        },
    )
    .ok();
}
