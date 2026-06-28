// upload_worker.rs
// 职责：文件上传 worker 池。
// 只做调度：从 channel 取任务 → 调 api::file + api::sync，不含路由字符串。
use crate::api::{
    client::ApiClient,
    file::api as file_api,
    sync::{api as sync_api, params::NotifyParams},
};
use crate::config::SharedSyncConfig;
use reqwest::multipart;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::path::PathBuf;
use tauri::{AppHandle, Emitter};
use tokio::sync::mpsc;

/// 一次上传任务
#[derive(Debug)]
pub struct UploadTask {
    pub local_path: PathBuf,
    pub remote_dir: String,
    pub folder_id: u64,
    pub relative_path: String,
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

    let (server_url, token, device_id) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone(), cfg.device_id.clone())
    };
    if server_url.is_empty() || token.is_empty() {
        return;
    }

    let client = ApiClient::new(&server_url, &token);

    let file_name = match task.local_path.file_name() {
        Some(n) => n.to_string_lossy().to_string(),
        None => return,
    };

    // 读文件 + 计算 sha256
    let data = match tokio::fs::read(&task.local_path).await {
        Ok(d) => d,
        Err(e) => {
            emit_progress(app, &path_str, "error", Some(e.to_string()));
            return;
        }
    };
    let file_hash = {
        let mut h = Sha256::new();
        h.update(&data);
        hex::encode(h.finalize())
    };
    let file_size = data.len() as i64;

    emit_progress(app, &path_str, "uploading", None);

    // 1. 上传文件字节（multipart）
    let file_part = multipart::Part::bytes(data)
        .file_name(file_name.clone())
        .mime_str("application/octet-stream")
        .unwrap();
    let form = multipart::Form::new()
        .text("path", task.remote_dir.clone())
        .text("name", file_name.clone())
        .text("action", "upload")
        .part("file", file_part);

    match file_api::upload_file(&client, form).await {
        Ok(resp) if resp.is_ok() => {}
        Ok(resp) => {
            crate::logger::error("upload", format!("上传失败 {}: {}", path_str, resp.message));
            emit_progress(app, &path_str, "error", Some(resp.message));
            return;
        }
        Err(e) => {
            crate::logger::error("upload", format!("上传失败 {}: {}", path_str, e));
            emit_progress(app, &path_str, "error", Some(e));
            return;
        }
    }

    // 2. 上传成功后上报 file_changed（带 base_hash 供服务端 CAS）
    let mtime = task
        .local_path
        .metadata()
        .ok()
        .and_then(|m| m.modified().ok())
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_secs() as i64);

    let folder_id = task.folder_id;
    let rel = task.relative_path.clone();
    let base_hash = crate::base_store::get(folder_id, &rel);

    sync_api::notify(
        &client,
        NotifyParams {
            device_id,
            folder_id,
            relative_path: rel.clone(),
            file_name,
            action: "modify".into(),
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
    crate::base_store::set(folder_id, &rel, &file_hash);
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
