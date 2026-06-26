mod sync_config;
mod sync_engine;
mod upload_worker;
mod watcher;
mod ws_client;

use std::path::PathBuf;
use sync_config::{FolderMapping, SharedSyncConfig, SyncConfig, init_sync_config};
use sync_engine::{SharedSyncEngine, engine_watch_path, init_sync_engine, start_sync_engine, stop_sync_engine};
use tauri::State;
use watcher::{SharedWatcherState, add_watch_path, init_watcher_state, list_watch_paths, remove_watch_path};

// ── 基础文件监听 commands（前端手动监听，不上传） ──────────────────────────

#[tauri::command]
fn add_watch(
    path: String,
    state: State<SharedWatcherState>,
    app_handle: tauri::AppHandle,
) -> Result<(), String> {
    let p = PathBuf::from(&path);
    if !p.exists() {
        return Err(format!("路径不存在: {}", path));
    }
    if !p.is_dir() {
        return Err(format!("路径不是目录: {}", path));
    }
    add_watch_path(&state, p, app_handle)
}

#[tauri::command]
fn remove_watch(path: String, state: State<SharedWatcherState>) -> Result<(), String> {
    remove_watch_path(&state, &PathBuf::from(&path))
}

#[tauri::command]
fn list_watches(state: State<SharedWatcherState>) -> Vec<String> {
    list_watch_paths(&state)
}

// ── 同步引擎 commands ────────────────────────────────────────────────────────

/// 设置服务器配置 + 认证 token
#[tauri::command]
fn set_sync_config(
    server_url: String,
    ws_url: String,
    token: String,
    upload_workers: Option<usize>,
    debounce_ms: Option<u64>,
    config: State<SharedSyncConfig>,
) {
    let mut cfg = config.write();
    cfg.server_url = server_url;
    cfg.ws_url = ws_url;
    cfg.token = token;
    if let Some(w) = upload_workers {
        cfg.upload_workers = w;
    }
    if let Some(d) = debounce_ms {
        cfg.debounce_ms = d;
    }
}

/// 读取当前同步配置（token 脱敏）
#[tauri::command]
fn get_sync_config(config: State<SharedSyncConfig>) -> SyncConfig {
    let mut cfg = config.read().clone();
    cfg.token = if cfg.token.is_empty() {
        String::new()
    } else {
        "***".into()
    };
    cfg
}

/// 添加目录映射：本地路径 ↔ 服务器路径（folder_id 从服务端注册后传入）
#[tauri::command]
fn add_folder_mapping(
    local_path: String,
    remote_path: String,
    folder_id: u64,
    config: State<SharedSyncConfig>,
    engine: State<SharedSyncEngine>,
) -> Result<(), String> {
    let p = PathBuf::from(&local_path);
    if !p.exists() || !p.is_dir() {
        return Err(format!("本地路径不存在或不是目录: {}", local_path));
    }

    {
        let mut cfg = config.write();
        if cfg.folder_mappings.iter().any(|m| m.local_path == local_path) {
            return Err(format!("目录已添加: {}", local_path));
        }
        cfg.folder_mappings.push(FolderMapping {
            local_path: local_path.clone(),
            remote_path,
            folder_id,
        });
    }

    // 如果引擎已启动，追加监听
    let guard = engine.lock();
    if guard.is_some() {
        drop(guard);
        engine_watch_path(&engine, p)?;
    }

    Ok(())
}

/// 移除目录映射
#[tauri::command]
fn remove_folder_mapping(local_path: String, config: State<SharedSyncConfig>) {
    let mut cfg = config.write();
    cfg.folder_mappings.retain(|m| m.local_path != local_path);
}

/// 启动同步引擎（watcher + 上传 worker + WS 客户端）
#[tauri::command]
fn start_sync(
    engine: State<SharedSyncEngine>,
    config: State<SharedSyncConfig>,
    app_handle: tauri::AppHandle,
) -> Result<(), String> {
    start_sync_engine(&engine, config.inner().clone(), app_handle.clone())?;

    // 把已配置的目录全部加入 watcher
    let paths: Vec<PathBuf> = config
        .read()
        .folder_mappings
        .iter()
        .map(|m| PathBuf::from(&m.local_path))
        .collect();

    for p in paths {
        engine_watch_path(&engine, p).ok();
    }

    Ok(())
}

/// 停止同步引擎
#[tauri::command]
fn stop_sync(engine: State<SharedSyncEngine>) {
    stop_sync_engine(&engine);
}

/// 引擎是否在运行
#[tauri::command]
fn is_sync_running(engine: State<SharedSyncEngine>) -> bool {
    engine.lock().is_some()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(init_watcher_state())
        .manage(init_sync_config())
        .manage(init_sync_engine())
        .invoke_handler(tauri::generate_handler![
            // 基础监听
            add_watch,
            remove_watch,
            list_watches,
            // 同步引擎
            set_sync_config,
            get_sync_config,
            add_folder_mapping,
            remove_folder_mapping,
            start_sync,
            stop_sync,
            is_sync_running,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
