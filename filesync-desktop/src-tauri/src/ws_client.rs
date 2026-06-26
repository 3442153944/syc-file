use crate::sync_config::SharedSyncConfig;
use crate::upload_worker::UploadTask;
use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::path::PathBuf;
use std::time::Duration;
use tauri::{AppHandle, Emitter};
use tokio::sync::mpsc;
use tokio::time::sleep;
use tokio_tungstenite::{connect_async, tungstenite::Message};

// ── WS 消息结构 ────────────────────────────────────────────────────────────

#[derive(Debug, Deserialize)]
struct WsEnvelope {
    #[serde(rename = "type")]
    kind: String,
    content: Option<Value>,
}

/// task_created content 字段
#[derive(Debug, Deserialize)]
struct TaskCreatedContent {
    event: String,
    task_id: u64,
    task_type: String,
    folder_id: u64,
    relative_path: String,
    file_name: String,
    file_size: Option<i64>,
    file_hash: Option<String>,
    remote_path: Option<String>,
    remote_dir: Option<String>,
}

/// conflict content 字段
#[derive(Debug, Deserialize)]
struct ConflictContent {
    folder_id: u64,
    relative_path: String,
    file_name: String,
    server_hash: String,
    local_hash: String,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WsStatus {
    pub connected: bool,
    pub message: String,
}

// ── 启动 WS 客户端 ──────────────────────────────────────────────────────────

pub fn start_ws_client(
    config: SharedSyncConfig,
    _upload_tx: mpsc::Sender<UploadTask>,
    app: AppHandle,
) {
    tokio::spawn(run_ws_loop(config, app));
}

async fn run_ws_loop(config: SharedSyncConfig, app: AppHandle) {
    let mut backoff = 1u64;
    loop {
        let (ws_url, token, device_id, device_name) = {
            let cfg = config.read();
            (
                cfg.ws_url.clone(),
                cfg.token.clone(),
                cfg.device_id.clone(),
                cfg.device_name.clone(),
            )
        };

        if ws_url.is_empty() || token.is_empty() {
            sleep(Duration::from_secs(5)).await;
            continue;
        }

        // 带 device_id、device_type 等参数连接（协议 §2.1）
        let url = format!(
            "{}?token={}&device_id={}&device_type=desktop&device_name={}&platform=windows",
            ws_url,
            token,
            device_id,
            urlenc(&device_name)
        );

        match connect_async(&url).await {
            Ok((ws_stream, _)) => {
                backoff = 1;
                emit_ws_status(&app, true, "已连接到服务器");

                handle_ws_session(ws_stream, &config, &app).await;

                emit_ws_status(&app, false, "连接断开，正在重连...");
            }
            Err(e) => {
                emit_ws_status(&app, false, &format!("连接失败: {}", e));
            }
        }

        sleep(Duration::from_secs(backoff.min(30))).await;
        backoff = (backoff * 2).min(30);
    }
}

async fn handle_ws_session(
    mut ws_stream: impl StreamExt<Item = Result<Message, tokio_tungstenite::tungstenite::Error>>
        + SinkExt<Message>
        + Unpin,
    config: &SharedSyncConfig,
    app: &AppHandle,
) {
    while let Some(msg) = ws_stream.next().await {
        match msg {
            Ok(Message::Text(text)) => {
                on_text_message(&text, config, app).await;
            }
            Ok(Message::Ping(data)) => {
                ws_stream.send(Message::Pong(data)).await.ok();
            }
            Ok(Message::Close(_)) | Err(_) => break,
            _ => {}
        }
    }
}

async fn on_text_message(text: &str, config: &SharedSyncConfig, app: &AppHandle) {
    let envelope: WsEnvelope = match serde_json::from_str(text) {
        Ok(v) => v,
        Err(_) => return,
    };

    if envelope.kind != "file_sync" {
        return;
    }

    let content = match envelope.content {
        Some(c) => c,
        None => return,
    };

    // 取 event 字段决定分支
    let event = content
        .get("event")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();

    match event.as_str() {
        "task_created" => {
            if let Ok(tc) = serde_json::from_value::<TaskCreatedContent>(content) {
                on_task_created(tc, config, app).await;
            }
        }
        "conflict" => {
            if let Ok(cf) = serde_json::from_value::<ConflictContent>(content) {
                on_conflict(cf, config, app).await;
            }
        }
        _ => {}
    }
}

// ── task_created 处理 ───────────────────────────────────────────────────────

async fn on_task_created(tc: TaskCreatedContent, config: &SharedSyncConfig, app: &AppHandle) {
    match tc.task_type.as_str() {
        "download" => {
            let remote_dir = tc.remote_dir.clone().unwrap_or_default();
            let local_dir = resolve_local_dir(config, tc.folder_id, &tc.relative_path);
            if local_dir.is_none() {
                report_task_failed(config, tc.task_id, "未找到对应本地目录映射", app).await;
                return;
            }
            let local_dir = local_dir.unwrap();

            let (server_url, token) = {
                let cfg = config.read();
                (cfg.server_url.clone(), cfg.token.clone())
            };

            tokio::spawn(do_download(
                tc.task_id,
                server_url,
                token,
                remote_dir,
                tc.file_name,
                tc.file_hash,
                local_dir,
                app.clone(),
                config.clone(),
            ));
        }
        "delete" => {
            let local_path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    report_task_failed(config, tc.task_id, "未找到本地文件路径", app).await;
                    return;
                }
            };
            match tokio::fs::remove_file(&local_path).await {
                Ok(_) | Err(_) => {
                    // 不存在也视为成功
                    report_task_completed(config, tc.task_id, "", app).await;
                    app.emit(
                        "sync-event",
                        serde_json::json!({ "path": local_path.to_string_lossy(), "kind": "deleted_by_server" }),
                    )
                    .ok();
                }
            }
        }
        "mkdir" => {
            let local_path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    report_task_failed(config, tc.task_id, "未找到本地目录路径", app).await;
                    return;
                }
            };
            match tokio::fs::create_dir_all(&local_path).await {
                Ok(_) => {
                    report_task_completed(config, tc.task_id, "", app).await;
                }
                Err(e) => {
                    report_task_failed(config, tc.task_id, &e.to_string(), app).await;
                }
            }
        }
        _ => {}
    }
}

async fn do_download(
    task_id: u64,
    server_url: String,
    token: String,
    remote_dir: String,
    file_name: String,
    expected_hash: Option<String>,
    local_dir: PathBuf,
    app: AppHandle,
    config: SharedSyncConfig,
) {
    let save_path = local_dir.join(&file_name);
    let path_str = save_path.to_string_lossy().to_string();

    app.emit(
        "download-progress",
        serde_json::json!({ "path": path_str, "status": "downloading", "taskId": task_id }),
    )
    .ok();

    let url = format!(
        "{}/v1/file/download?path={}&name={}&token={}",
        server_url,
        urlenc(&remote_dir),
        urlenc(&file_name),
        token
    );

    let client = reqwest::Client::new();
    let resp = match client.get(&url).send().await {
        Ok(r) if r.status().is_success() => r,
        Ok(r) => {
            let msg = format!("HTTP {}", r.status());
            app.emit(
                "download-progress",
                serde_json::json!({ "path": path_str, "status": "error", "error": msg }),
            )
            .ok();
            report_task_failed(&config, task_id, &msg, &app).await;
            return;
        }
        Err(e) => {
            app.emit(
                "download-progress",
                serde_json::json!({ "path": path_str, "status": "error", "error": e.to_string() }),
            )
            .ok();
            report_task_failed(&config, task_id, &e.to_string(), &app).await;
            return;
        }
    };

    let bytes = match resp.bytes().await {
        Ok(b) => b,
        Err(e) => {
            report_task_failed(&config, task_id, &e.to_string(), &app).await;
            return;
        }
    };

    // 路径穿越防护：local_dir 必须是 save_path 的前缀
    let canonical_dir = tokio::fs::canonicalize(&local_dir).await;
    if let Ok(cdir) = canonical_dir {
        if let Some(parent) = save_path.parent() {
            if let Ok(cparent) = tokio::fs::canonicalize(parent).await {
                if !cparent.starts_with(&cdir) {
                    report_task_failed(&config, task_id, "路径穿越检测拒绝", &app).await;
                    return;
                }
            }
        }
    }

    if let Some(parent) = save_path.parent() {
        tokio::fs::create_dir_all(parent).await.ok();
    }

    if let Err(e) = tokio::fs::write(&save_path, &bytes).await {
        report_task_failed(&config, task_id, &e.to_string(), &app).await;
        return;
    }

    // 计算落盘文件 hash，回传给服务端校验
    let actual_hash = {
        let mut h = Sha256::new();
        h.update(&bytes);
        hex::encode(h.finalize())
    };

    if let Some(exp) = &expected_hash {
        if exp != &actual_hash {
            let msg = format!("hash 不匹配: expected={} actual={}", exp, actual_hash);
            app.emit(
                "download-progress",
                serde_json::json!({ "path": path_str, "status": "hash_mismatch", "error": msg }),
            )
            .ok();
            report_task_failed(&config, task_id, &msg, &app).await;
            return;
        }
    }

    app.emit(
        "download-progress",
        serde_json::json!({ "path": path_str, "status": "done", "taskId": task_id }),
    )
    .ok();

    report_task_completed(&config, task_id, &actual_hash, &app).await;
}

// ── conflict 处理 ───────────────────────────────────────────────────────────

async fn on_conflict(cf: ConflictContent, config: &SharedSyncConfig, app: &AppHandle) {
    // 找到本地文件，改名加 .conflict.<ts>
    let local_path = match resolve_local_file(config, cf.folder_id, &cf.relative_path) {
        Some(p) => p,
        None => return,
    };

    let ts = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    let conflict_path = local_path.with_extension(format!(
        "{}.conflict.{}",
        local_path.extension().and_then(|e| e.to_str()).unwrap_or(""),
        ts
    ));

    if local_path.exists() {
        tokio::fs::rename(&local_path, &conflict_path).await.ok();
    }

    app.emit(
        "sync-conflict",
        serde_json::json!({
            "folderID": cf.folder_id,
            "relativePath": cf.relative_path,
            "fileName": cf.file_name,
            "serverHash": cf.server_hash,
            "localHash": cf.local_hash,
            "conflictFile": conflict_path.to_string_lossy(),
        }),
    )
    .ok();
}

// ── 任务回调 ────────────────────────────────────────────────────────────────

async fn report_task_completed(config: &SharedSyncConfig, task_id: u64, file_hash: &str, _app: &AppHandle) {
    let (server_url, token) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone())
    };
    if server_url.is_empty() {
        return;
    }
    let url = format!("{}/v1/sync/tasks/{}/complete", server_url, task_id);
    reqwest::Client::new()
        .post(&url)
        .header("Token", &token)
        .json(&serde_json::json!({ "file_hash": file_hash }))
        .send()
        .await
        .ok();
}

async fn report_task_failed(config: &SharedSyncConfig, task_id: u64, error: &str, _app: &AppHandle) {
    let (server_url, token) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone())
    };
    if server_url.is_empty() {
        return;
    }
    let url = format!("{}/v1/sync/tasks/{}/failed", server_url, task_id);
    reqwest::Client::new()
        .post(&url)
        .header("Token", &token)
        .json(&serde_json::json!({ "error": error }))
        .send()
        .await
        .ok();
}

// ── 路径解析工具 ─────────────────────────────────────────────────────────────

/// 根据 folder_id + relative_path 找到本地目录（文件所在的父目录）
fn resolve_local_dir(config: &SharedSyncConfig, folder_id: u64, relative_path: &str) -> Option<PathBuf> {
    let cfg = config.read();
    let mapping = cfg.folder_mappings.iter().find(|m| m.folder_id == folder_id)?;
    let rel_dir = PathBuf::from(relative_path)
        .parent()
        .map(|p| p.to_path_buf())
        .unwrap_or_default();
    Some(PathBuf::from(&mapping.local_path).join(rel_dir))
}

/// 根据 folder_id + relative_path 找到本地完整文件路径
fn resolve_local_file(config: &SharedSyncConfig, folder_id: u64, relative_path: &str) -> Option<PathBuf> {
    let cfg = config.read();
    let mapping = cfg.folder_mappings.iter().find(|m| m.folder_id == folder_id)?;
    // 防路径穿越：过滤 .. 段
    let safe_rel: PathBuf = PathBuf::from(relative_path)
        .components()
        .filter(|c| matches!(c, std::path::Component::Normal(_)))
        .collect();
    Some(PathBuf::from(&mapping.local_path).join(safe_rel))
}

// ── 工具函数 ─────────────────────────────────────────────────────────────────

fn emit_ws_status(app: &AppHandle, connected: bool, message: &str) {
    app.emit(
        "ws-status",
        WsStatus {
            connected,
            message: message.to_string(),
        },
    )
    .ok();
}

fn urlenc(s: &str) -> String {
    s.chars()
        .flat_map(|c| {
            if c.is_alphanumeric() || matches!(c, '-' | '_' | '.' | '/' | ':' | '\\') {
                vec![c]
            } else {
                format!("%{:02X}", c as u32).chars().collect()
            }
        })
        .collect()
}
