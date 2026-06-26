use crate::sync_config::SharedSyncConfig;
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
                let task = {
                    let mut guard = rx.lock().await;
                    guard.recv().await
                };
                match task {
                    None => break,
                    Some(t) => upload_file(&t, &config, &app).await,
                }
            }
        });
    }

    tx
}

async fn upload_file(task: &UploadTask, config: &SharedSyncConfig, app: &AppHandle) {
    let path_str = task.local_path.to_string_lossy().to_string();

    let (server_url, token, device_id) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone(), cfg.device_id.clone())
    };

    if server_url.is_empty() || token.is_empty() {
        return;
    }

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
        let mut hasher = Sha256::new();
        hasher.update(&data);
        hex::encode(hasher.finalize())
    };
    let file_size = data.len() as i64;

    emit_progress(app, &path_str, "uploading", None);

    // 1. 先上传文件字节
    let file_part = multipart::Part::bytes(data.clone())
        .file_name(file_name.clone())
        .mime_str("application/octet-stream")
        .unwrap();

    let form = multipart::Form::new()
        .text("path", task.remote_dir.clone())
        .text("name", file_name.clone())
        .text("action", "upload")
        .part("file", file_part);

    let client = reqwest::Client::new();
    let upload_url = format!("{}/v1/file/upload", server_url);

    let upload_ok = match client
        .post(&upload_url)
        .header("Token", &token)
        .multipart(form)
        .send()
        .await
    {
        Ok(resp) if resp.status().is_success() => true,
        Ok(resp) => {
            emit_progress(app, &path_str, "error", Some(format!("HTTP {}", resp.status())));
            false
        }
        Err(e) => {
            emit_progress(app, &path_str, "error", Some(e.to_string()));
            false
        }
    };

    if !upload_ok {
        return;
    }

    // 2. 上传成功后，上报 file_changed（WS 或 HTTP /sync/notify 回退）
    let mtime = task
        .local_path
        .metadata()
        .ok()
        .and_then(|m| m.modified().ok())
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);

    let notify_body = serde_json::json!({
        "device_id": device_id,
        "folder_id": task.folder_id,
        "relative_path": task.relative_path,
        "file_name": file_name,
        "action": "modify",
        "file_size": file_size,
        "file_hash": file_hash,
        "is_dir": false,
        "mtime": mtime,
    });

    let notify_url = format!("{}/v1/sync/notify", server_url);
    client
        .post(&notify_url)
        .header("Token", &token)
        .json(&notify_body)
        .send()
        .await
        .ok();

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
