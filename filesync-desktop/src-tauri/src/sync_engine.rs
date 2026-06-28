use crate::config::SharedSyncConfig;
use crate::api::{client::ApiClient, sync::api as sync_api};
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
                } else {
                    crate::logger::debug("watch", format!("删除不在同步目录内，已忽略: {}", path.display()));
                }
            }
            "create" | "modify" => {
                if !path.is_file() {
                    continue;
                }
                if let Some((folder_id, rel, remote_dir)) = find_mapping_with_remote(config, &path) {
                    crate::logger::info("watch", format!("检测到变更，准备上传: {}", rel));
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
                } else {
                    crate::logger::warn("watch", format!("变更不在任何同步目录内，已忽略: {}", path.display()));
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
    use crate::api::sync::params::NotifyParams;

    let (server_url, token, device_id) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone(), cfg.device_id.clone())
    };
    if server_url.is_empty() || token.is_empty() {
        return;
    }

    let file_name = path.file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_default();
    let client = ApiClient::new(&server_url, &token);

    sync_api::notify(&client, NotifyParams {
        device_id,
        folder_id,
        relative_path: relative_path.to_string(),
        file_name,
        action: "delete".into(),
        file_size: None,
        file_hash: None,
        base_hash: None,
        is_dir: false,
        mtime: None,
    }).await.ok();
    crate::base_store::remove(folder_id, relative_path);
    crate::logger::info("watch", format!("已上报删除: {}", relative_path));

    app.emit("sync-event", SyncEvent {
        path: path.to_string_lossy().to_string(),
        kind: "delete".into(),
    }).ok();
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

// ── 首次全量同步 ─────────────────────────────────────────────────────────────

/// 遍历所有映射目录下、尚无同步基线的文件，逐个入上传队列。
/// 上传由 worker 池并发处理（数量 = upload_workers）；上传后各文件上报 file_changed，
/// 服务端据此把本地内容并入 trunk 并派发给其它设备。
/// 已有 base 基线的文件视为已同步而跳过，避免每次启动重复全量上传。
pub fn engine_enqueue_initial_sync(engine: &SharedSyncEngine, config: &SharedSyncConfig) {
    let tx = {
        let guard = engine.lock();
        match guard.as_ref() {
            Some(e) => e.upload_tx.clone(),
            None => return,
        }
    };
    let mappings: Vec<(u64, PathBuf, String)> = config
        .read()
        .folder_mappings
        .iter()
        .map(|m| (m.folder_id, PathBuf::from(&m.local_path), m.remote_path.clone()))
        .collect();

    tokio::spawn(async move {
        let mut count = 0usize;
        for (folder_id, root, remote_root) in mappings {
            count += walk_and_enqueue(&tx, folder_id, &root, &remote_root).await;
        }
        crate::logger::info("sync", format!("首次全量同步已入队 {} 个文件", count));
    });
}

/// 递归遍历目录，把「无 base 基线」的文件入上传队列；返回入队数量。
async fn walk_and_enqueue(
    tx: &mpsc::Sender<UploadTask>,
    folder_id: u64,
    root: &PathBuf,
    remote_root: &str,
) -> usize {
    let mut count = 0usize;
    let mut stack = vec![root.clone()];
    while let Some(dir) = stack.pop() {
        let mut rd = match tokio::fs::read_dir(&dir).await {
            Ok(r) => r,
            Err(_) => continue,
        };
        while let Ok(Some(entry)) = rd.next_entry().await {
            let path = entry.path();
            if should_ignore(&path) {
                continue;
            }
            let ft = match entry.file_type().await {
                Ok(t) => t,
                Err(_) => continue,
            };
            if ft.is_dir() {
                stack.push(path);
            } else if ft.is_file() {
                if let Some((rel, remote_dir)) = rel_and_remote(root, remote_root, &path) {
                    // 已有基线视为已同步，跳过，避免重复上传
                    if crate::base_store::get(folder_id, &rel).is_some() {
                        continue;
                    }
                    let task = UploadTask {
                        local_path: path,
                        remote_dir,
                        folder_id,
                        relative_path: rel,
                    };
                    if tx.send(task).await.is_ok() {
                        count += 1;
                    }
                }
            }
        }
    }
    count
}

fn rel_and_remote(root: &PathBuf, remote_root: &str, path: &PathBuf) -> Option<(String, String)> {
    let rel = path.strip_prefix(root).ok()?;
    let rel_str = rel.to_string_lossy().replace('\\', "/");
    let rel_dir = rel
        .parent()
        .map(|p| p.to_string_lossy().replace('\\', "/"))
        .unwrap_or_default();
    let base = remote_root.trim_end_matches('/');
    let remote_dir = if rel_dir.is_empty() {
        base.to_string()
    } else {
        format!("{}/{}", base, rel_dir)
    };
    Some((rel_str, remote_dir))
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
