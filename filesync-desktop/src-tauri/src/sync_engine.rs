use crate::sync_config::SharedSyncConfig;
use crate::upload_worker::{start_upload_workers, UploadTask};
use crate::ws_client::start_ws_client;
use notify::{Event, EventKind, RecommendedWatcher, RecursiveMode, Watcher};
use parking_lot::Mutex;
use serde::Serialize;
use std::collections::HashMap;
use std::path::{PathBuf, Component};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tauri::{AppHandle, Emitter};
use tokio::sync::mpsc;
use tokio::time::sleep;

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SyncEvent {
    pub path: String,
    pub kind: String,
}

type DebounceMap = Arc<Mutex<HashMap<PathBuf, (String, Instant)>>>;

pub struct SyncEngine {
    pub watcher: RecommendedWatcher,
    #[allow(dead_code)]
    upload_tx: mpsc::Sender<UploadTask>,
}

pub type SharedSyncEngine = Arc<Mutex<Option<SyncEngine>>>;

pub fn init_sync_engine() -> SharedSyncEngine {
    Arc::new(Mutex::new(None))
}

pub fn start_sync_engine(
    engine: &SharedSyncEngine,
    config: SharedSyncConfig,
    app: AppHandle,
) -> Result<(), String> {
    let mut guard = engine.lock();
    if guard.is_some() {
        return Err("同步引擎已在运行".into());
    }

    let (worker_count, debounce_ms) = {
        let cfg = config.read();
        (cfg.upload_workers, cfg.debounce_ms)
    };

    let upload_tx = start_upload_workers(worker_count, config.clone(), app.clone());
    start_ws_client(config.clone(), upload_tx.clone(), app.clone());

    let debounce_map: DebounceMap = Arc::new(Mutex::new(HashMap::new()));

    // 防抖刷新器
    {
        let dm = debounce_map.clone();
        let cfg = config.clone();
        let tx = upload_tx.clone();
        let app2 = app.clone();
        tokio::spawn(async move {
            loop {
                sleep(Duration::from_millis(50)).await;
                flush_debounce(&dm, &cfg, &tx, &app2, debounce_ms).await;
            }
        });
    }

    // 文件 watcher
    let dm = debounce_map.clone();
    let watcher = notify::recommended_watcher(move |res: Result<Event, notify::Error>| {
        let event = match res {
            Ok(e) => e,
            Err(_) => return,
        };
        let kind_str = match event.kind {
            EventKind::Create(_) => "create",
            EventKind::Modify(_) => "modify",
            EventKind::Remove(_) => "remove",
            _ => return,
        };
        let mut map = dm.lock();
        for path in event.paths {
            if should_ignore(&path) {
                continue;
            }
            // 同路径取最新 kind（modify 覆盖 create 没关系；remove 要保留）
            let entry = map.entry(path).or_insert((kind_str.to_string(), Instant::now()));
            entry.0 = kind_str.to_string();
            entry.1 = Instant::now();
        }
    })
    .map_err(|e| e.to_string())?;

    *guard = Some(SyncEngine { watcher, upload_tx });
    Ok(())
}

async fn flush_debounce(
    dm: &DebounceMap,
    config: &SharedSyncConfig,
    tx: &mpsc::Sender<UploadTask>,
    app: &AppHandle,
    debounce_ms: u64,
) {
    let now = Instant::now();
    let threshold = Duration::from_millis(debounce_ms);

    let ready: Vec<(PathBuf, String)> = {
        let mut map = dm.lock();
        let ready: Vec<_> = map
            .iter()
            .filter(|(_, (_, t))| now.duration_since(*t) >= threshold)
            .map(|(p, (k, _))| (p.clone(), k.clone()))
            .collect();
        for (p, _) in &ready {
            map.remove(p);
        }
        ready
    };

    for (path, kind) in ready {
        match kind.as_str() {
            "remove" => {
                // 上报删除事件给服务端（不上传文件）
                if let Some((folder_id, rel)) = find_mapping(config, &path) {
                    report_delete(config, folder_id, &rel, &path, app).await;
                }
            }
            "create" | "modify" => {
                if !path.is_file() {
                    continue;
                }
                if let Some((folder_id, rel, remote_dir)) = find_mapping_with_remote(config, &path) {
                    app.emit(
                        "sync-event",
                        SyncEvent {
                            path: path.to_string_lossy().to_string(),
                            kind: kind.clone(),
                        },
                    )
                    .ok();
                    tx.send(UploadTask {
                        local_path: path,
                        remote_dir,
                        folder_id,
                        relative_path: rel,
                    })
                    .await
                    .ok();
                }
            }
            _ => {}
        }
    }
}

async fn report_delete(
    config: &SharedSyncConfig,
    folder_id: u64,
    relative_path: &str,
    path: &PathBuf,
    app: &AppHandle,
) {
    let (server_url, token, device_id) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone(), cfg.device_id.clone())
    };
    if server_url.is_empty() || token.is_empty() {
        return;
    }

    let file_name = path
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .unwrap_or_default();

    let body = serde_json::json!({
        "device_id": device_id,
        "folder_id": folder_id,
        "relative_path": relative_path,
        "file_name": file_name,
        "action": "delete",
        "is_dir": false,
    });

    reqwest::Client::new()
        .post(format!("{}/v1/sync/notify", server_url))
        .header("Token", &token)
        .json(&body)
        .send()
        .await
        .ok();

    app.emit(
        "sync-event",
        SyncEvent {
            path: path.to_string_lossy().to_string(),
            kind: "delete".into(),
        },
    )
    .ok();
}

pub fn engine_watch_path(engine: &SharedSyncEngine, path: PathBuf) -> Result<(), String> {
    let mut guard = engine.lock();
    let eng = guard.as_mut().ok_or("同步引擎未启动")?;
    eng.watcher
        .watch(&path, RecursiveMode::Recursive)
        .map_err(|e| e.to_string())
}

pub fn stop_sync_engine(engine: &SharedSyncEngine) {
    let mut guard = engine.lock();
    *guard = None;
}

// ── 路径解析 ─────────────────────────────────────────────────────────────────

fn find_mapping_with_remote(
    config: &SharedSyncConfig,
    path: &PathBuf,
) -> Option<(u64, String, String)> {
    let cfg = config.read();
    for m in &cfg.folder_mappings {
        let base = PathBuf::from(&m.local_path);
        if let Ok(rel) = path.strip_prefix(&base) {
            let rel_str = rel.to_string_lossy().replace('\\', "/");
            let rel_dir = rel
                .parent()
                .map(|p| p.to_string_lossy().replace('\\', "/"))
                .unwrap_or_default();
            let remote_dir = if rel_dir.is_empty() {
                m.remote_path.trim_end_matches('/').to_string()
            } else {
                format!("{}/{}", m.remote_path.trim_end_matches('/'), rel_dir)
            };
            return Some((m.folder_id, rel_str, remote_dir));
        }
    }
    None
}

fn find_mapping(config: &SharedSyncConfig, path: &PathBuf) -> Option<(u64, String)> {
    let cfg = config.read();
    for m in &cfg.folder_mappings {
        let base = PathBuf::from(&m.local_path);
        if let Ok(rel) = path.strip_prefix(&base) {
            let rel_str = rel.to_string_lossy().replace('\\', "/");
            return Some((m.folder_id, rel_str));
        }
    }
    None
}

// ── 过滤规则 ─────────────────────────────────────────────────────────────────

fn should_ignore(path: &PathBuf) -> bool {
    let name = match path.file_name() {
        Some(n) => n.to_string_lossy().to_lowercase(),
        None => return true,
    };
    if name.ends_with(".tmp")
        || name.ends_with(".swp")
        || name.starts_with('~')
        || name.starts_with('.')
        || name == "thumbs.db"
        || name == "desktop.ini"
    {
        return true;
    }
    // 路径中有隐藏目录也跳过
    for c in path.components() {
        if let Component::Normal(s) = c {
            if s.to_string_lossy().starts_with('.') {
                return true;
            }
        }
    }
    false
}
