// ws_client.rs
// 职责：WebSocket 连接管理 + 消息路由。
// 解析 WsEnvelope → 按 event 分发 → 调 api::sync 执行任务回调，不含路由字符串。
use crate::api::{
    client::ApiClient,
    file::params::DownloadParams,
    file::api as file_api,
    sync::api as sync_api,
    ws::types::{ConflictContent, TaskCreatedContent, WsEnvelope},
};
use crate::config::SharedSyncConfig;
use crate::upload_worker::UploadTask;
use futures_util::{SinkExt, StreamExt};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::path::{Component, PathBuf};
use std::time::Duration;
use tauri::{AppHandle, Emitter};
use tokio::sync::mpsc;
use tokio::time::sleep;
use tokio_tungstenite::{connect_async, tungstenite::Message};

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WsStatus {
    pub connected: bool,
    pub message: String,
}

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

        // 协议 §2.1：连接时带 device_id/device_type/platform
        let url = format!(
            "{}?token={}&device_id={}&device_type=desktop&device_name={}&platform=windows",
            ws_url, token, device_id, urlenc(&device_name)
        );

        match connect_async(&url).await {
            Ok((ws_stream, _)) => {
                backoff = 1;
                emit_ws_status(&app, true, "已连接到服务器");
                handle_session(ws_stream, &config, &app).await;
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

async fn handle_session(
    mut ws: impl StreamExt<Item = Result<Message, tokio_tungstenite::tungstenite::Error>>
        + SinkExt<Message>
        + Unpin,
    config: &SharedSyncConfig,
    app: &AppHandle,
) {
    while let Some(msg) = ws.next().await {
        match msg {
            Ok(Message::Text(text)) => on_text(&text, config, app).await,
            Ok(Message::Ping(d)) => { ws.send(Message::Pong(d)).await.ok(); }
            Ok(Message::Close(_)) | Err(_) => break,
            _ => {}
        }
    }
}

async fn on_text(text: &str, config: &SharedSyncConfig, app: &AppHandle) {
    let env: WsEnvelope = match serde_json::from_str(text) {
        Ok(v) => v,
        Err(_) => return,
    };
    if env.kind != "file_sync" {
        return;
    }
    let content = match env.content {
        Some(c) => c,
        None => return,
    };
    let event = content.get("event").and_then(|v| v.as_str()).unwrap_or("").to_string();

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

// ── task_created ─────────────────────────────────────────────────────────────

async fn on_task_created(tc: TaskCreatedContent, config: &SharedSyncConfig, app: &AppHandle) {
    let (server_url, token) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone())
    };
    let client = ApiClient::new(&server_url, &token);

    match tc.task_type.as_str() {
        "download" => {
            let remote_dir = tc.remote_dir.clone().unwrap_or_default();
            let local_dir = match resolve_local_dir(config, tc.folder_id, &tc.relative_path) {
                Some(d) => d,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地目录映射").await.ok();
                    return;
                }
            };
            tokio::spawn(do_download(
                tc.task_id, client, remote_dir, tc.file_name,
                tc.file_hash, local_dir, app.clone(),
            ));
        }
        "delete" => {
            let path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地文件").await.ok();
                    return;
                }
            };
            // 不存在也视为成功
            tokio::fs::remove_file(&path).await.ok();
            sync_api::complete_task(&client, tc.task_id, "").await.ok();
            app.emit("sync-event", serde_json::json!({
                "path": path.to_string_lossy(), "kind": "deleted_by_server"
            })).ok();
        }
        "mkdir" => {
            let path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地路径").await.ok();
                    return;
                }
            };
            match tokio::fs::create_dir_all(&path).await {
                Ok(_) => { sync_api::complete_task(&client, tc.task_id, "").await.ok(); }
                Err(e) => { sync_api::fail_task(&client, tc.task_id, &e.to_string()).await.ok(); }
            }
        }
        _ => {}
    }
}

async fn do_download(
    task_id: u64,
    client: ApiClient,
    remote_dir: String,
    file_name: String,
    expected_hash: Option<String>,
    local_dir: PathBuf,
    app: AppHandle,
) {
    let save_path = local_dir.join(&file_name);
    let path_str = save_path.to_string_lossy().to_string();

    app.emit("download-progress", serde_json::json!({
        "path": path_str, "status": "downloading", "taskId": task_id
    })).ok();

    // 构建下载 URL（token 放 query string）
    let url = file_api::build_download_url(&client, &DownloadParams {
        path: remote_dir,
        name: file_name,
        device_id: String::new(),
    });

    let resp = match reqwest::get(&url).await {
        Ok(r) if r.status().is_success() => r,
        Ok(r) => {
            let msg = format!("HTTP {}", r.status());
            sync_api::fail_task(&client, task_id, &msg).await.ok();
            app.emit("download-progress", serde_json::json!({
                "path": path_str, "status": "error", "error": msg
            })).ok();
            return;
        }
        Err(e) => {
            sync_api::fail_task(&client, task_id, &e.to_string()).await.ok();
            return;
        }
    };

    let bytes = match resp.bytes().await {
        Ok(b) => b,
        Err(e) => {
            sync_api::fail_task(&client, task_id, &e.to_string()).await.ok();
            return;
        }
    };

    // 路径穿越防护
    if let (Ok(cdir), Some(parent)) = (
        tokio::fs::canonicalize(&local_dir).await,
        save_path.parent(),
    ) {
        tokio::fs::create_dir_all(parent).await.ok();
        if let Ok(cparent) = tokio::fs::canonicalize(parent).await {
            if !cparent.starts_with(&cdir) {
                sync_api::fail_task(&client, task_id, "路径穿越拒绝").await.ok();
                return;
            }
        }
    }

    if let Some(parent) = save_path.parent() {
        tokio::fs::create_dir_all(parent).await.ok();
    }
    if let Err(e) = tokio::fs::write(&save_path, &bytes).await {
        sync_api::fail_task(&client, task_id, &e.to_string()).await.ok();
        return;
    }

    let actual_hash = {
        let mut h = Sha256::new();
        h.update(&bytes);
        hex::encode(h.finalize())
    };

    if let Some(exp) = &expected_hash {
        if exp != &actual_hash {
            let msg = format!("hash 不匹配: expected={} actual={}", exp, actual_hash);
            sync_api::fail_task(&client, task_id, &msg).await.ok();
            app.emit("download-progress", serde_json::json!({
                "path": path_str, "status": "hash_mismatch", "error": msg
            })).ok();
            return;
        }
    }

    sync_api::complete_task(&client, task_id, &actual_hash).await.ok();
    app.emit("download-progress", serde_json::json!({
        "path": path_str, "status": "done", "taskId": task_id
    })).ok();
}

// ── conflict ─────────────────────────────────────────────────────────────────

async fn on_conflict(cf: ConflictContent, config: &SharedSyncConfig, app: &AppHandle) {
    let local_path = match resolve_local_file(config, cf.folder_id, &cf.relative_path) {
        Some(p) => p,
        None => return,
    };
    let ts = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let ext = local_path.extension().and_then(|e| e.to_str()).unwrap_or("");
    let conflict_path = local_path.with_extension(format!("{}.conflict.{}", ext, ts));
    if local_path.exists() {
        tokio::fs::rename(&local_path, &conflict_path).await.ok();
    }
    app.emit("sync-conflict", serde_json::json!({
        "folderID": cf.folder_id,
        "relativePath": cf.relative_path,
        "fileName": cf.file_name,
        "serverHash": cf.server_hash,
        "localHash": cf.local_hash,
        "conflictFile": conflict_path.to_string_lossy(),
    })).ok();
}

// ── 路径工具 ─────────────────────────────────────────────────────────────────

fn resolve_local_dir(config: &SharedSyncConfig, folder_id: u64, relative_path: &str) -> Option<PathBuf> {
    let cfg = config.read();
    let m = cfg.folder_mappings.iter().find(|m| m.folder_id == folder_id)?;
    let rel_dir = PathBuf::from(relative_path).parent().map(|p| p.to_path_buf()).unwrap_or_default();
    Some(PathBuf::from(&m.local_path).join(rel_dir))
}

fn resolve_local_file(config: &SharedSyncConfig, folder_id: u64, relative_path: &str) -> Option<PathBuf> {
    let cfg = config.read();
    let m = cfg.folder_mappings.iter().find(|m| m.folder_id == folder_id)?;
    let safe: PathBuf = PathBuf::from(relative_path)
        .components()
        .filter(|c| matches!(c, Component::Normal(_)))
        .collect();
    Some(PathBuf::from(&m.local_path).join(safe))
}

// ── 工具函数 ─────────────────────────────────────────────────────────────────

fn emit_ws_status(app: &AppHandle, connected: bool, message: &str) {
    app.emit("ws-status", WsStatus { connected, message: message.into() }).ok();
}

fn urlenc(s: &str) -> String {
    s.chars().flat_map(|c| {
        if c.is_alphanumeric() || matches!(c, '-' | '_' | '.') { vec![c] }
        else { format!("%{:02X}", c as u32).chars().collect() }
    }).collect()
}
