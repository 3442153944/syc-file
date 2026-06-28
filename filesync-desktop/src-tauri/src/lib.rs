mod api;
mod app_paths;
mod config;
mod device;
mod logger;
mod sync_engine;
mod upload_worker;
mod watcher;
mod ws_client;
mod base_store;

use std::path::PathBuf;
use api::client::ApiClient;
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};
use api::user::{api as user_api, params::*, response::*};
use api::file::{api as file_api, params::*, response::*};
use api::sync::{api as sync_api, params::*, response::*};
use config::{FolderMapping, SharedSyncConfig, SyncConfig, init_sync_config};
use sync_engine::{SharedSyncEngine, engine_enqueue_initial_sync, engine_watch_path, init_sync_engine, start_sync_engine, stop_sync_engine};
use tauri::State;
use watcher::{SharedWatcherState, add_watch_path, init_watcher_state, list_watch_paths, remove_watch_path};

// ── 辅助 ──────────────────────────────────────────────────────────────────────

fn make_client(cfg: &SyncConfig) -> Result<ApiClient, String> {
    if cfg.server_url.is_empty() {
        return Err("服务器地址未配置，请先在设置页面填写服务器地址".into());
    }
    Ok(ApiClient::new(&cfg.server_url, &cfg.token))
}

fn api_data<T>(resp: api::client::ApiResponse<T>, op: &str) -> Result<T, String> {
    if resp.is_ok() {
        resp.data.ok_or_else(|| format!("{}: 响应 data 为空", op))
    } else {
        Err(format!("{}: {}", op, resp.message))
    }
}

// ── 基础文件监听（监控页面用，不触发上传） ────────────────────────────────────

#[tauri::command]
fn add_watch(
    path: String,
    state: State<SharedWatcherState>,
    app_handle: tauri::AppHandle,
) -> Result<(), String> {
    let p = PathBuf::from(&path);
    if !p.exists() { return Err(format!("路径不存在: {}", path)); }
    if !p.is_dir() { return Err(format!("路径不是目录: {}", path)); }
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

// ── 同步配置 ─────────────────────────────────────────────────────────────────

#[tauri::command]
fn set_sync_config(
    server_url: String,
    ws_url: String,
    token: String,
    upload_workers: Option<usize>,
    download_workers: Option<usize>,
    debounce_ms: Option<u64>,
    config: State<SharedSyncConfig>,
) {
    let mut cfg = config.write();
    cfg.server_url = server_url;
    cfg.ws_url = ws_url;
    cfg.token = token;
    if let Some(w) = upload_workers { cfg.upload_workers = w; }
    if let Some(w) = download_workers { cfg.download_workers = w; }
    if let Some(d) = debounce_ms { cfg.debounce_ms = d; }
    cfg.save(); // 持久化到 config/config.yml（token 不写入）
    logger::info("config", "同步配置已更新并保存");
}

// ── 日志窗口 ─────────────────────────────────────────────────────────────────

/// 打开（或聚焦）专用日志窗口，label = "logs"，渲染前端 LogViewer。
fn open_log_window(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("logs") {
        let _ = w.set_focus();
        return;
    }
    match WebviewWindowBuilder::new(app, "logs", WebviewUrl::App("index.html".into()))
        .title("云梯 - 日志")
        .inner_size(960.0, 560.0)
        .build()
    {
        Ok(_) => logger::info("app", "已打开日志窗口"),
        Err(e) => logger::error("app", format!("打开日志窗口失败: {}", e)),
    }
}

#[tauri::command]
fn open_log_window_cmd(app: tauri::AppHandle) {
    open_log_window(&app);
}

/// 返回当前配置（token 脱敏）
#[tauri::command]
fn get_sync_config(config: State<SharedSyncConfig>) -> SyncConfig {
    let mut cfg = config.read().clone();
    cfg.token = if cfg.token.is_empty() { String::new() } else { "***".into() };
    cfg
}

/// 返回设备 ID（供前端在注册 folder 时传给服务端）
#[tauri::command]
fn get_device_id(config: State<SharedSyncConfig>) -> String {
    config.read().device_id.clone()
}

// ── 同步引擎 ─────────────────────────────────────────────────────────────────

/// 添加目录映射（folder_id 为服务端注册后返回的 SyncFolder.id）
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
        cfg.folder_mappings.push(FolderMapping { local_path: local_path.clone(), remote_path, folder_id });
    }
    let guard = engine.lock();
    if guard.is_some() {
        drop(guard);
        engine_watch_path(&engine, p)?;
    }
    Ok(())
}

#[tauri::command]
fn remove_folder_mapping(local_path: String, config: State<SharedSyncConfig>) {
    config.write().folder_mappings.retain(|m| m.local_path != local_path);
}

/// 启动同步引擎：先从服务器拉取 sync_folders 填充内存缓存，再启动引擎和文件监听
#[tauri::command]
async fn start_sync(
    engine: State<'_, SharedSyncEngine>,
    config: State<'_, SharedSyncConfig>,
    app_handle: tauri::AppHandle,
) -> Result<(), String> {
    // 1. 从服务器拉最新 folder 列表，刷新内存缓存
    let client = make_client(&config.read())?;
    let resp = sync_api::list_folders(&client).await?;
    if resp.is_ok() {
        if let Some(folders) = resp.data {
            let mut cfg = config.write();
            cfg.folder_mappings = folders
                .into_iter()
                .filter(|f| f.enabled)
                .map(|f| FolderMapping {
                    local_path: f.local_path,
                    remote_path: f.remote_path,
                    folder_id: f.id,
                })
                .collect();
        }
    }

    // 2. 启动引擎
    start_sync_engine(&engine, config.inner().clone(), app_handle)?;

    // 3. 监听所有本地路径
    let paths: Vec<PathBuf> = config.read().folder_mappings
        .iter().map(|m| PathBuf::from(&m.local_path)).collect();
    if paths.is_empty() {
        logger::warn("sync", "未配置任何同步目录，引擎已启动但不会监听任何文件。请先在「同步管理」创建同步文件夹。");
    }
    for p in &paths {
        match engine_watch_path(&engine, p.clone()) {
            Ok(_) => logger::info("sync", format!("开始监听同步目录: {}", p.display())),
            Err(e) => logger::error("sync", format!("监听目录失败 {}: {}", p.display(), e)),
        }
    }

    // 4. 首次全量同步：把尚无基线的本地文件入上传队列（多线程上传）
    engine_enqueue_initial_sync(&engine, config.inner());
    logger::info("sync", format!("同步引擎已启动，监听 {} 个目录", paths.len()));
    Ok(())
}

#[tauri::command]
fn stop_sync(engine: State<SharedSyncEngine>) {
    stop_sync_engine(&engine);
}

#[tauri::command]
fn is_sync_running(engine: State<SharedSyncEngine>) -> bool {
    engine.lock().is_some()
}

// ── 用户域 commands ───────────────────────────────────────────────────────────

/// 登录：成功后把 token 写入 SyncConfig
#[tauri::command]
async fn login(
    username: String,
    password: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<LoginData, String> {
    let client = make_client(&config.read())?;
    let resp = user_api::login(&client, LoginParams { username, password }).await?;
    let data = api_data(resp, "login")?;
    config.write().token = data.token.clone();
    Ok(data)
}

/// 用当前 config 里的 token 验证登录态
#[tauri::command]
async fn verify(config: State<'_, SharedSyncConfig>) -> Result<VerifyData, String> {
    let client = make_client(&config.read())?;
    let resp = user_api::verify(&client).await?;
    api_data(resp, "verify")
}

#[tauri::command]
async fn register(
    username: String,
    password: String,
    email: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let resp = user_api::register(&client, RegisterParams { username, password, email }).await?;
    api_data(resp, "register")
}

#[tauri::command]
async fn reset_password(
    username: String,
    old_password: String,
    new_password: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let resp = user_api::reset_password(&client, ResetPasswordParams { username, old_password, new_password }).await?;
    api_data(resp, "reset_password")
}

// ── 文件域 commands ───────────────────────────────────────────────────────────

#[tauri::command]
async fn get_available_disks(config: State<'_, SharedSyncConfig>) -> Result<AvailableDisksData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::get_available_disks(&client, AvailableDisksParams { disk_path: String::new(), detailed: true }).await?;
    api_data(resp, "get_available_disks")
}

#[tauri::command]
async fn traverse_directory(
    path: String,
    page: i32,
    page_size: i32,
    config: State<'_, SharedSyncConfig>,
) -> Result<TraverseDirectoryData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::traverse_directory(&client, TraverseDirectoryParams { path, page, page_size }).await?;
    api_data(resp, "traverse_directory")
}

/// 上传文件：TS 传本地绝对路径，Rust 读文件构造 multipart
#[tauri::command]
async fn upload_file(
    local_path: String,
    remote_dir: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<UploadData, String> {
    use reqwest::multipart;
    let path = std::path::Path::new(&local_path);
    let name = path.file_name()
        .ok_or_else(|| format!("无效路径: {}", local_path))?
        .to_string_lossy()
        .to_string();
    let bytes = tokio::fs::read(&local_path).await.map_err(|e| e.to_string())?;
    let part = multipart::Part::bytes(bytes)
        .file_name(name.clone())
        .mime_str("application/octet-stream")
        .map_err(|e| e.to_string())?;
    let form = multipart::Form::new()
        .text("action", "upload")
        .text("path", remote_dir)
        .text("name", name)
        .part("file", part);
    let client = make_client(&config.read())?;
    let resp = file_api::upload_file(&client, form).await?;
    api_data(resp, "upload_file")
}

/// 删除远端文件（文件管理用）
#[tauri::command]
async fn delete_file(
    path: String,
    name: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = file_api::delete_file(&client, api::file::params::DeleteFileParams { path, name }).await?;
    if resp.is_ok() { Ok(()) } else { Err(resp.message) }
}

/// 构建带 token 的完整下载 URL，前端可直接用于下载
#[tauri::command]
fn build_download_url(
    path: String,
    name: String,
    device_id: String,
    config: State<SharedSyncConfig>,
) -> Result<String, String> {
    let client = make_client(&config.read())?;
    Ok(file_api::build_download_url(&client, &DownloadParams { path, name, device_id }))
}

#[tauri::command]
async fn get_download_history(
    page_num: i32,
    page_size: i32,
    config: State<'_, SharedSyncConfig>,
) -> Result<DownloadHistoryData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::get_download_history(&client, DownloadHistoryParams { page_num, page_size }).await?;
    api_data(resp, "get_download_history")
}

#[tauri::command]
async fn delete_download_history(
    ids: Vec<i64>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = file_api::delete_download_history(&client, DeleteDownloadHistoryParams { ids }).await?;
    if resp.is_ok() { Ok(()) } else { Err(resp.message) }
}

// ── 同步域 commands ───────────────────────────────────────────────────────────

#[tauri::command]
async fn create_sync_folder(
    name: String,
    local_path: String,
    remote_path: String,
    direction: String,
    config: State<'_, SharedSyncConfig>,
    engine: State<'_, SharedSyncEngine>,
) -> Result<SyncFolder, String> {
    let (device_id, client) = {
        let cfg = config.read();
        (cfg.device_id.clone(), make_client(&cfg)?)
    };
    let params = CreateFolderParams { name, local_path: local_path.clone(), remote_path: remote_path.clone(), direction, owner_device_id: device_id };
    let resp = sync_api::create_folder(&client, params).await?;
    let folder = api_data(resp, "create_sync_folder")?;

    // 自动更新内存缓存
    {
        let mut cfg = config.write();
        if !cfg.folder_mappings.iter().any(|m| m.local_path == local_path) {
            cfg.folder_mappings.push(FolderMapping {
                local_path: local_path.clone(),
                remote_path,
                folder_id: folder.id,
            });
        }
    }
    // 如果引擎已在运行，立即开始监听新目录
    if engine.lock().is_some() {
        engine_watch_path(&engine, PathBuf::from(&local_path)).ok();
    }

    Ok(folder)
}

#[tauri::command]
async fn list_sync_folders(config: State<'_, SharedSyncConfig>) -> Result<Vec<SyncFolder>, String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::list_folders(&client).await?;
    api_data(resp, "list_sync_folders")
}

#[tauri::command]
async fn delete_sync_folder(
    folder_id: u64,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::delete_folder(&client, folder_id).await?;
    if !resp.is_ok() { return Err(resp.message); }
    // 从内存缓存移除（停止对该目录的上传调度；watcher 不主动 unwatch，重启后自然消失）
    config.write().folder_mappings.retain(|m| m.folder_id != folder_id);
    Ok(())
}

#[tauri::command]
async fn list_pending_tasks(config: State<'_, SharedSyncConfig>) -> Result<Vec<SyncTask>, String> {
    let (device_id, client) = {
        let cfg = config.read();
        (cfg.device_id.clone(), make_client(&cfg)?)
    };
    let resp = sync_api::list_pending_tasks(&client, &device_id).await?;
    api_data(resp, "list_pending_tasks")
}

#[tauri::command]
async fn list_conflicts(config: State<'_, SharedSyncConfig>) -> Result<Vec<SyncConflict>, String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::list_conflicts(&client).await?;
    api_data(resp, "list_conflicts")
}

/// 解决冲突：resolution = accept_server / keep_local
#[tauri::command]
async fn resolve_conflict(
    conflict_id: u64,
    resolution: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::resolve_conflict(&client, conflict_id, &resolution).await?;
    if resp.is_ok() { Ok(()) } else { Err(resp.message) }
}

#[tauri::command]
async fn delete_conflict(
    conflict_id: u64,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::delete_conflict(&client, conflict_id).await?;
    if resp.is_ok() { Ok(()) } else { Err(resp.message) }
}

// ── Tauri 入口 ────────────────────────────────────────────────────────────────

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(init_watcher_state())
        .manage(init_sync_config())
        .manage(init_sync_engine())
        .setup(|app| {
            // 绑定全局日志（事件 + 文件），任意模块即可 logger::info(...)
            logger::init(app.handle().clone());
            base_store::init(); // 载入 base_hash 基线
            logger::info(
                "app",
                format!("应用启动，配置目录: {}", app_paths::config_dir().display()),
            );

            // 顶部菜单栏：一级菜单「工具」→「打开日志窗口」(Ctrl+Alt+T)
            use tauri::menu::{MenuBuilder, MenuItemBuilder, SubmenuBuilder};
            let open_logs = MenuItemBuilder::with_id("open_logs", "打开日志窗口")
                .accelerator("CmdOrCtrl+Alt+T")
                .build(app)?;
            let tools = SubmenuBuilder::new(app, "工具").item(&open_logs).build()?;
            let menu = MenuBuilder::new(app).item(&tools).build()?;
            app.set_menu(menu)?;
            Ok(())
        })
        .on_menu_event(|app, event| {
            if event.id().as_ref() == "open_logs" {
                open_log_window(app);
            }
        })
        .invoke_handler(tauri::generate_handler![
            // 基础监听
            add_watch, remove_watch, list_watches,
            // 配置
            set_sync_config, get_sync_config, get_device_id,
            // 日志
            open_log_window_cmd,
            // 同步引擎
            add_folder_mapping, remove_folder_mapping,
            start_sync, stop_sync, is_sync_running,
            // 用户域
            login, verify, register, reset_password,
            // 文件域
            get_available_disks, traverse_directory, upload_file, delete_file,
            build_download_url, get_download_history, delete_download_history,
            // 同步域
            create_sync_folder, list_sync_folders, delete_sync_folder,
            list_pending_tasks, list_conflicts, resolve_conflict, delete_conflict,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
