// ws_client.rs
// 职责：WebSocket 连接管理 + 同步消息路由 + 任务执行。
// 下载走「写 .synctmp → 校验 hash → 原子 rename」原子发布；目标被占用回 task_blocked；
// 冲突时把本地分叉隔离到 .syncpending 并收敛服务端版本；下载并发由信号量限流。
use crate::api::{
    client::ApiClient,
    file::api as file_api,
    file::params::{DeleteFileParams, DownloadParams},
    sync::api as sync_api,
    sync::params::NotifyParams,
    ws::types::{ConflictContent, ConflictResolvedContent, TaskCreatedContent, WsEnvelope},
};
use crate::base_store;
use crate::catch_up;
use crate::chunked_uploader::{self, UploadOptions};
use crate::config::SharedSyncConfig;
use crate::logger;
use crate::upload_worker::UploadTask;
use futures_util::{SinkExt, StreamExt};
use parking_lot::Mutex;
use serde::Serialize;
use std::collections::HashMap;
use std::path::{Component, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::{Arc, OnceLock};
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager};
use tokio::sync::{mpsc, Semaphore};
use tokio::time::sleep;
use tokio_tungstenite::{connect_async, tungstenite::Message};

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WsStatus {
    pub connected: bool,
    pub message: String,
}

/// 下载并发信号量（首次全量同步、多小文件时限流，避免打爆磁盘/网络）。
static DL_SEM: OnceLock<Arc<Semaphore>> = OnceLock::new();

fn dl_sem(config: &SharedSyncConfig) -> Arc<Semaphore> {
    DL_SEM
        .get_or_init(|| {
            let n = config.read().download_workers.max(1);
            Arc::new(Semaphore::new(n))
        })
        .clone()
}

/// 待处理冲突：conflict_id → 隔离副本信息，供 keep_local 重新提交。
struct PendingConflict {
    folder_id: u64,
    relative_path: String,
    file_name: String,
    remote_dir: String,
    quarantine: PathBuf,
}

static PENDING: OnceLock<Mutex<HashMap<u64, PendingConflict>>> = OnceLock::new();

fn pending() -> &'static Mutex<HashMap<u64, PendingConflict>> {
    PENDING.get_or_init(|| Mutex::new(HashMap::new()))
}

/// 原子发布的错误分类：Locked = 目标被占用（转 waiting_unlock），Other = 真失败。
enum PublishErr {
    Locked,
    Other(String),
}

pub fn start_ws_client(
    config: SharedSyncConfig,
    upload_tx: mpsc::Sender<UploadTask>,
    app: AppHandle,
) {
    tokio::spawn(run_ws_loop(config, upload_tx, app));
}

/// 重连间隔：固定 3 秒，**不做指数退避、不限次数**。
/// WS 是同步链路的刚需（task_created 全靠它推），退避到 30s 只会让断网恢复后白等半分钟。
const RECONNECT_INTERVAL: Duration = Duration::from_secs(3);

/// 当前会话的出站发送端。前端通过 Tauri 命令要往 WS 上发东西（如订阅监控），都经这里。
/// 会话建立时装上、断开时清掉；没有活动会话时发送返回 false。
static WS_OUTBOUND: OnceLock<Mutex<Option<mpsc::UnboundedSender<Message>>>> = OnceLock::new();

fn ws_outbound() -> &'static Mutex<Option<mpsc::UnboundedSender<Message>>> {
    WS_OUTBOUND.get_or_init(|| Mutex::new(None))
}

/// 往当前 WS 会话发一帧文本。无活动会话返回 false（调用方据此决定是否稍后重试）。
pub fn ws_send_text(json: String) -> bool {
    if let Some(tx) = ws_outbound().lock().as_ref() {
        return tx.send(Message::Text(json)).is_ok();
    }
    false
}

/// 监控订阅意向：断线重连后 connID 会变、服务端旧订阅随之失效，
/// 所以要在每次新会话建立时自动补发一次 subscribe。0 = 未订阅，>0 = 订阅且为推送间隔（秒）。
static MONITOR_SUB_INTERVAL: AtomicI64 = AtomicI64::new(0);

/// 设置/取消监控订阅意向，并立即对当前会话生效。interval<=0 表示退订。
pub fn set_monitor_subscription(interval: i64) {
    MONITOR_SUB_INTERVAL.store(interval.max(0), Ordering::SeqCst);
    let frame = if interval > 0 {
        serde_json::json!({"type":"monitor","content":{"event":"subscribe","interval":interval}})
    } else {
        serde_json::json!({"type":"monitor","content":{"event":"unsubscribe"}})
    };
    ws_send_text(frame.to_string());
}

/// 新会话建立后调用：若之前订阅了监控，自动补发 subscribe。
fn resubscribe_monitor() {
    let interval = MONITOR_SUB_INTERVAL.load(Ordering::SeqCst);
    if interval > 0 {
        let frame = serde_json::json!({
            "type":"monitor","content":{"event":"subscribe","interval":interval}
        });
        ws_send_text(frame.to_string());
    }
}

async fn run_ws_loop(
    config: SharedSyncConfig,
    upload_tx: mpsc::Sender<UploadTask>,
    app: AppHandle,
) {
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

        // 还没配服务器 / 还没登录：没什么可连的，等下一轮
        if ws_url.is_empty() || token.is_empty() {
            sleep(RECONNECT_INTERVAL).await;
            continue;
        }

        // 协议 §2.1：连接时带 device_id/device_type/platform
        let url = format!(
            "{}/v1/ws/connect?token={}&device_id={}&device_type=desktop&device_name={}&platform=windows",
            ws_url.trim_end_matches('/'), token, device_id, urlenc(&device_name)
        );

        match connect_async(&url).await {
            Ok((ws_stream, _)) => {
                logger::info("ws", "已连接到服务器");
                emit_ws_status(&app, true, "已连接到服务器");
                // 装上本会话的出站通道，供前端命令（订阅监控等）往 WS 发消息
                let (out_tx, out_rx) = mpsc::unbounded_channel::<Message>();
                *ws_outbound().lock() = Some(out_tx);
                handle_session(ws_stream, out_rx, &config, &upload_tx, &app).await;
                // 会话结束：清掉出站通道，之后的 ws_send_text 会返回 false
                *ws_outbound().lock() = None;
                logger::warn("ws", "连接断开，正在重连…");
                emit_ws_status(&app, false, "连接断开，正在重连...");
            }
            Err(e) => {
                logger::error("ws", format!("连接失败: {}", e));
                emit_ws_status(&app, false, &format!("连接失败: {}", e));
            }
        }

        sleep(RECONNECT_INTERVAL).await;
    }
}

async fn handle_session(
    mut ws: impl StreamExt<Item = Result<Message, tokio_tungstenite::tungstenite::Error>>
        + SinkExt<Message>
        + Unpin,
    mut out_rx: mpsc::UnboundedReceiver<Message>,
    config: &SharedSyncConfig,
    upload_tx: &mpsc::Sender<UploadTask>,
    app: &AppHandle,
) {
    catch_up::catch_up_all_folders(config, upload_tx, app).await;
    // 断线重连后 connID 变了，服务端旧订阅已失效——重新补发一次监控订阅
    resubscribe_monitor();

    // 读写双路：既收服务端推送，又把前端要发的帧（订阅监控等）写出去。
    loop {
        tokio::select! {
            inbound = ws.next() => {
                match inbound {
                    Some(Ok(Message::Text(text))) => on_text(&text, config, app).await,
                    Some(Ok(Message::Ping(d))) => { ws.send(Message::Pong(d)).await.ok(); }
                    Some(Ok(Message::Close(_))) | Some(Err(_)) | None => break,
                    _ => {}
                }
            }
            outbound = out_rx.recv() => {
                match outbound {
                    Some(frame) => { ws.send(frame).await.ok(); }
                    None => {} // 发送端全部 drop，理论上不会发生（静态持有）
                }
            }
        }
    }
}

async fn on_text(text: &str, config: &SharedSyncConfig, app: &AppHandle) {
    let env: WsEnvelope = match serde_json::from_str(text) {
        Ok(v) => v,
        Err(_) => return,
    };
    // 剪贴板：服务端单向投递（上报走 REST），内容就是一条 ClipItem
    if env.kind == "clipboard" {
        if let Some(c) = env.content {
            on_clipboard(c, app);
        }
        return;
    }
    // 监控：服务端定时推送的指标帧，原样转成 Tauri 事件给监控页
    if env.kind == "monitor" {
        if let Some(c) = env.content {
            let _ = app.emit("monitor-metrics", c);
        }
        return;
    }
    if env.kind != "file_sync" {
        return;
    }
    let content = match env.content {
        Some(c) => c,
        None => return,
    };
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
        "conflict_resolved" => {
            if let Ok(cr) = serde_json::from_value::<ConflictResolvedContent>(content) {
                on_conflict_resolved(cr, config, app).await;
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
            logger::info("task", format!("收到下载任务: {}", tc.relative_path));
            tokio::spawn(do_download(
                tc.task_id,
                client,
                config.clone(),
                remote_dir,
                tc.folder_id,
                tc.relative_path,
                tc.file_name,
                tc.file_hash,
                app.clone(),
            ));
        }
        "delete" => {
            let path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地文件")
                        .await
                        .ok();
                    return;
                }
            };
            crate::sync_engine::mute_path(&path); // 防回环：这是引擎自己执行的删除，不是用户手删
            tokio::fs::remove_file(&path).await.ok(); // 不存在也视为成功
            base_store::remove(tc.folder_id, &tc.relative_path);
            sync_api::complete_task(&client, tc.task_id, "").await.ok();
            logger::info("task", format!("已删除本地文件: {}", tc.relative_path));
            app.emit(
                "sync-event",
                serde_json::json!({
                    "path": path.to_string_lossy(), "kind": "deleted_by_server"
                }),
            )
            .ok();
        }
        "mkdir" => {
            let path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地路径")
                        .await
                        .ok();
                    return;
                }
            };
            match tokio::fs::create_dir_all(&path).await {
                Ok(_) => {
                    sync_api::complete_task(&client, tc.task_id, "").await.ok();
                    logger::info("task", format!("已创建本地目录: {}", tc.relative_path));
                }
                Err(e) => {
                    sync_api::fail_task(&client, tc.task_id, &e.to_string())
                        .await
                        .ok();
                }
            }
        }
        _ => {}
    }
}

#[allow(clippy::too_many_arguments)]
async fn do_download(
    task_id: u64,
    client: ApiClient,
    config: SharedSyncConfig,
    remote_dir: String,
    folder_id: u64,
    relative_path: String,
    file_name: String,
    expected_hash: Option<String>,
    app: AppHandle,
) {
    let _permit = dl_sem(&config).acquire_owned().await.ok(); // 并发限流

    let local_root = match resolve_local_root(&config, folder_id) {
        Some(r) => r,
        None => {
            sync_api::fail_task(&client, task_id, "未找到本地目录映射")
                .await
                .ok();
            return;
        }
    };
    let safe_rel = sanitize_rel(&relative_path);
    let final_str = local_root.join(&safe_rel).to_string_lossy().to_string();

    app.emit(
        "download-progress",
        serde_json::json!({
            "path": final_str, "status": "downloading", "taskId": task_id
        }),
    )
    .ok();

    match download_and_publish(
        &client,
        remote_dir,
        &file_name,
        expected_hash.as_deref(),
        &local_root,
        &safe_rel,
    )
    .await
    {
        Ok(hash) => {
            sync_api::complete_task(&client, task_id, &hash).await.ok();
            base_store::set_with_file(
                folder_id,
                &relative_path,
                &hash,
                &local_root.join(&safe_rel),
            );
            logger::info("download", format!("已下载并发布: {}", relative_path));
            app.emit(
                "download-progress",
                serde_json::json!({
                    "path": final_str, "status": "done", "taskId": task_id
                }),
            )
            .ok();
        }
        Err(PublishErr::Locked) => {
            sync_api::block_task(&client, task_id, "目标文件被占用")
                .await
                .ok();
            logger::warn(
                "download",
                format!("目标被占用，转等待解锁: {}", relative_path),
            );
            app.emit(
                "download-progress",
                serde_json::json!({
                    "path": final_str, "status": "blocked"
                }),
            )
            .ok();
        }
        Err(PublishErr::Other(msg)) => {
            sync_api::fail_task(&client, task_id, &msg).await.ok();
            logger::error("download", format!("下载失败 {}: {}", relative_path, msg));
            app.emit(
                "download-progress",
                serde_json::json!({
                    "path": final_str, "status": "error", "error": msg
                }),
            )
            .ok();
        }
    }
}

/// 下载字节 → 校验 hash → 写 .synctmp → 原子 rename 到主目录，返回落盘 hash。
async fn download_and_publish(
    client: &ApiClient,
    remote_dir: String,
    file_name: &str,
    expected_hash: Option<&str>,
    local_root: &PathBuf,
    safe_rel: &PathBuf,
) -> Result<String, PublishErr> {
    let url = file_api::build_download_url(
        client,
        &DownloadParams {
            path: remote_dir,
            name: file_name.to_string(),
            device_id: String::new(),
        },
    );

    let resp = reqwest::get(&url)
        .await
        .map_err(|e| PublishErr::Other(e.to_string()))?;
    if !resp.status().is_success() {
        return Err(PublishErr::Other(format!("HTTP {}", resp.status())));
    }
    let bytes = resp
        .bytes()
        .await
        .map_err(|e| PublishErr::Other(e.to_string()))?;

    // blake3 校验：算 blake3 hex 与 expected_hash 比对，不匹配直接失败。
    // expected_hash 为空字符串（trunk 尚未知道该文件 hash，比如刚被收编的远端已有文件）
    // 时不校验、直接采信下载内容——远端目录是权威源，此时没有可比对的基准。
    let actual = blake3_hex(&bytes);
    if let Some(exp) = expected_hash.filter(|s| !s.is_empty()) {
        if exp != actual {
            return Err(PublishErr::Other(format!(
                "hash 不匹配 expected={} actual={}",
                exp, actual
            )));
        }
    }

    // 写临时文件（.synctmp 已被 watcher 忽略）
    let tmp_dir = local_root.join(".synctmp");
    tokio::fs::create_dir_all(&tmp_dir).await.ok();
    let tmp_path = tmp_dir.join(format!("{}.{}.tmp", file_name, uuid::Uuid::new_v4()));
    tokio::fs::write(&tmp_path, &bytes)
        .await
        .map_err(|e| PublishErr::Other(e.to_string()))?;

    // 原子 rename 到主目录
    let final_path = local_root.join(safe_rel);
    if let Some(parent) = final_path.parent() {
        tokio::fs::create_dir_all(parent).await.ok();
    }
    crate::sync_engine::mute_path(&final_path); // 防回环：这是引擎自己落盘的内容，不是用户手改
    match tokio::fs::rename(&tmp_path, &final_path).await {
        Ok(_) => Ok(actual),
        Err(e) => {
            tokio::fs::remove_file(&tmp_path).await.ok();
            // Windows: 32=共享冲突(被占用) 5=拒绝访问
            match e.raw_os_error() {
                Some(32) | Some(5) => Err(PublishErr::Locked),
                _ => Err(PublishErr::Other(e.to_string())),
            }
        }
    }
}

// ── conflict ─────────────────────────────────────────────────────────────────

async fn on_conflict(cf: ConflictContent, config: &SharedSyncConfig, app: &AppHandle) {
    let local_root = match resolve_local_root(config, cf.folder_id) {
        Some(r) => r,
        None => return,
    };
    let safe_rel = sanitize_rel(&cf.relative_path);
    let main_path = local_root.join(&safe_rel);

    // 1) 隔离本地分叉到 .syncpending
    let pend_dir = local_root.join(".syncpending");
    tokio::fs::create_dir_all(&pend_dir).await.ok();
    let ts = now_secs();
    let quarantine = pend_dir.join(format!("{}.{}", cf.file_name, ts));
    if main_path.exists() {
        // 防回环：这一步只是把本地分叉挪去隔离区，不是用户删除，不能被 watcher 当真删除上报
        // （不然会把这次冲突处理误传播成"删除"指令派给其它设备）。
        crate::sync_engine::mute_path(&main_path);
        tokio::fs::rename(&main_path, &quarantine).await.ok();
    }

    // 2) 主目录收敛到服务端版本
    let remote_dir = build_remote_dir(config, cf.folder_id, &cf.relative_path);
    let (server_url, token) = {
        let c = config.read();
        (c.server_url.clone(), c.token.clone())
    };
    let client = ApiClient::new(&server_url, &token);
    match download_and_publish(
        &client,
        remote_dir.clone(),
        &cf.file_name,
        Some(cf.server_hash.as_str()),
        &local_root,
        &safe_rel,
    )
    .await
    {
        Ok(h) => base_store::set_with_file(
            cf.folder_id,
            &cf.relative_path,
            &h,
            &local_root.join(&safe_rel),
        ),
        Err(_) => logger::warn("conflict", "收敛服务端版本失败，将于重连扫描后补齐"),
    }

    // 3) 记待办（供 keep_local 重提交）+ 通知 UI
    if cf.conflict_id != 0 {
        pending().lock().insert(
            cf.conflict_id,
            PendingConflict {
                folder_id: cf.folder_id,
                relative_path: cf.relative_path.clone(),
                file_name: cf.file_name.clone(),
                remote_dir,
                quarantine: quarantine.clone(),
            },
        );
    }
    logger::warn(
        "conflict",
        format!("冲突：已隔离本地副本并收敛服务端版本: {}", cf.relative_path),
    );
    app.emit(
        "sync-conflict",
        serde_json::json!({
            "conflictId": cf.conflict_id,
            "folderId": cf.folder_id,
            "relativePath": cf.relative_path,
            "fileName": cf.file_name,
            "serverHash": cf.server_hash,
            "localHash": cf.local_hash,
            "quarantine": quarantine.to_string_lossy(),
        }),
    )
    .ok();
}

async fn on_conflict_resolved(
    cr: ConflictResolvedContent,
    config: &SharedSyncConfig,
    app: &AppHandle,
) {
    match cr.resolution.as_str() {
        "accept_server" => {
            let pc = pending().lock().remove(&cr.conflict_id); // 先取出，确保锁守卫在 await 前释放
            if let Some(pc) = pc {
                tokio::fs::remove_file(&pc.quarantine).await.ok();
            }
            logger::info(
                "conflict",
                format!("冲突 {} 已按服务端版本解决", cr.conflict_id),
            );
        }
        "keep_local" => {
            let pc = pending().lock().remove(&cr.conflict_id);
            match pc {
                Some(pc) => keep_local_reupload(config, pc, &cr.server_hash).await,
                None => logger::warn(
                    "conflict",
                    format!("冲突 {} keep_local 但找不到隔离副本", cr.conflict_id),
                ),
            }
        }
        _ => {}
    }
    app.emit(
        "sync-conflict-resolved",
        serde_json::json!({
            "conflictId": cr.conflict_id, "resolution": cr.resolution
        }),
    )
    .ok();
}

/// keep_local：以 server_hash 为 base，把隔离副本作为新版本上传，并把它放回主目录。
async fn keep_local_reupload(config: &SharedSyncConfig, pc: PendingConflict, server_hash: &str) {
    let (server_url, token, device_id) = {
        let c = config.read();
        (c.server_url.clone(), c.token.clone(), c.device_id.clone())
    };
    let client = ApiClient::new(&server_url, &token);

    // 隔离副本必须存在
    if !pc.quarantine.exists() {
        logger::error(
            "conflict",
            format!("keep_local 找不到隔离副本: {}", pc.quarantine.display()),
        );
        return;
    }

    // 1) 同步场景需覆盖：先删远端旧文件（收敛的服务端版本），再分片上传
    let _ = file_api::delete_file(
        &client,
        DeleteFileParams {
            path: pc.remote_dir.clone(),
            name: pc.file_name.clone(),
        },
    )
    .await;

    let options = UploadOptions::new(device_id.clone());
    let on_progress: chunked_uploader::ProgressFn = Arc::new(|_, _| {});
    let complete = match chunked_uploader::upload(
        &client,
        &pc.quarantine,
        &pc.remote_dir,
        &options,
        on_progress,
    )
    .await
    {
        Ok(d) => d,
        Err(e) => {
            logger::error("conflict", format!("keep_local 上传失败: {}", e));
            return;
        }
    };

    let hash = complete.file_hash.clone();
    let size = complete.file_size;

    // 2) 以 server_hash 为 base 上报 file_changed（快进，不再冲突）
    sync_api::notify(
        &client,
        NotifyParams {
            device_id,
            folder_id: pc.folder_id,
            relative_path: pc.relative_path.clone(),
            file_name: pc.file_name.clone(),
            action: "modify".into(),
            file_size: Some(size),
            file_hash: Some(hash.clone()),
            base_hash: Some(server_hash.to_string()),
            is_dir: false,
            mtime: Some(now_secs()),
        },
    )
    .await
    .ok();
    base_store::set_with_file(pc.folder_id, &pc.relative_path, &hash, &pc.quarantine);

    // 3) 把本地版本放回主目录（覆盖刚收敛的服务端版本）
    if let Some(root) = resolve_local_root(config, pc.folder_id) {
        let dest = root.join(sanitize_rel(&pc.relative_path));
        if let Some(parent) = dest.parent() {
            tokio::fs::create_dir_all(parent).await.ok();
        }
        crate::sync_engine::mute_path(&dest); // 防回环：内容跟基线一致，避免又被 watcher 当新变更
        tokio::fs::rename(&pc.quarantine, &dest).await.ok();
    }
    logger::info(
        "conflict",
        format!("冲突已按本地版本解决并重提交: {}", pc.relative_path),
    );
}

// ── 路径工具 ─────────────────────────────────────────────────────────────────

fn resolve_local_root(config: &SharedSyncConfig, folder_id: u64) -> Option<PathBuf> {
    let cfg = config.read();
    cfg.folder_mappings
        .iter()
        .find(|m| m.folder_id == folder_id)
        .map(|m| PathBuf::from(&m.local_path))
}

fn resolve_local_file(
    config: &SharedSyncConfig,
    folder_id: u64,
    relative_path: &str,
) -> Option<PathBuf> {
    let root = resolve_local_root(config, folder_id)?;
    Some(root.join(sanitize_rel(relative_path)))
}

/// 只保留普通路径段，丢弃 `..`/根，防止路径穿越。
fn sanitize_rel(relative_path: &str) -> PathBuf {
    PathBuf::from(relative_path)
        .components()
        .filter(|c| matches!(c, Component::Normal(_)))
        .collect()
}

/// 由 folder 映射 + 相对路径拼出服务端远端目录（用于下载 URL 的 path）。
fn build_remote_dir(config: &SharedSyncConfig, folder_id: u64, rel: &str) -> String {
    let cfg = config.read();
    if let Some(m) = cfg
        .folder_mappings
        .iter()
        .find(|m| m.folder_id == folder_id)
    {
        let rel_dir = PathBuf::from(rel)
            .parent()
            .map(|p| p.to_string_lossy().replace('\\', "/"))
            .unwrap_or_default();
        let base = m.remote_path.trim_end_matches('/');
        if rel_dir.is_empty() {
            base.to_string()
        } else {
            format!("{}/{}", base, rel_dir)
        }
    } else {
        String::new()
    }
}

// ── 工具函数 ─────────────────────────────────────────────────────────────────

fn blake3_hex(bytes: &[u8]) -> String {
    blake3::hash(bytes).to_hex().to_string()
}

fn now_secs() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// 当前 WS 连接状态（电平）。`ws-status` 事件是边沿信号，只在连上/断开的瞬间发一次，
/// 前端若在那一下之前还没注册监听（app 启动即连上，监听注册在其后）就会永远错过，
/// 状态卡在初始的「未连接」。所以这里额外保留一份电平，供前端注册后主动查询补齐。
static WS_CONNECTED: AtomicBool = AtomicBool::new(false);

/// 前端注册监听后主动查一次，补上可能已错过的连接事件。
pub fn ws_is_connected() -> bool {
    WS_CONNECTED.load(Ordering::SeqCst)
}

fn emit_ws_status(app: &AppHandle, connected: bool, message: &str) {
    WS_CONNECTED.store(connected, Ordering::SeqCst);
    app.emit(
        "ws-status",
        WsStatus {
            connected,
            message: message.into(),
        },
    )
    .ok();
}

fn urlenc(s: &str) -> String {
    s.chars()
        .flat_map(|c| {
            if c.is_alphanumeric() || matches!(c, '-' | '_' | '.') {
                vec![c]
            } else {
                format!("%{:02X}", c as u32).chars().collect()
            }
        })
        .collect()
}

/// 收到别的设备推来的剪贴板内容：始终发事件给前端（进历史列表），
/// 是否写入本机剪贴板由用户开关 auto_apply 决定。
fn on_clipboard(content: serde_json::Value, app: &AppHandle) {
    let item: crate::clipboard_sync::ClipItem = match serde_json::from_value(content) {
        Ok(v) => v,
        Err(e) => {
            logger::warn("clipboard", format!("解析剪贴板消息失败: {}", e));
            return;
        }
    };
    let state = app.state::<crate::clipboard_sync::SharedClipboardState>();
    let (enabled, auto_apply) = {
        let s = state.read();
        (s.enabled, s.auto_apply)
    };
    if !enabled {
        return; // 用户没开同步，收到也不处理
    }
    if auto_apply {
        match crate::clipboard_sync::apply_to_clipboard(&state, &item.content) {
            Ok(_) => logger::info(
                "clipboard",
                format!("已写入来自 {} 的剪贴板内容", item.device_name),
            ),
            Err(e) => logger::warn("clipboard", format!("写入剪贴板失败: {}", e)),
        }
    }
    let _ = app.emit("clipboard-received", item);
}
