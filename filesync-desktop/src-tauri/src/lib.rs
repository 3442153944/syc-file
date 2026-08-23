mod api;
mod app_paths;
mod base_store;
mod catch_up;
mod chunked_uploader;
mod clipboard_sync;
mod config;
mod device;
mod logger;
mod sync_engine;
mod upload_worker;
mod watcher;
mod ws_client;

use api::client::ApiClient;
use api::file::{api as file_api, params::*, response::*};
use api::sync::{api as sync_api, params::*, response::*};
use api::user::{api as user_api, params::*, response::*};
use config::{init_sync_config, FolderMapping, SharedSyncConfig, SyncConfig};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use sync_engine::{
    engine_enqueue_initial_sync, engine_watch_path, init_sync_engine, start_sync_engine,
    stop_sync_engine, SharedSyncEngine,
};
use tauri::State;
use tauri::{Emitter, Manager, WebviewUrl, WebviewWindowBuilder};
use watcher::{
    add_watch_path, init_watcher_state, list_watch_paths, remove_watch_path, SharedWatcherState,
};

// ── 辅助 ──────────────────────────────────────────────────────────────────────

/// 供其它模块（clipboard_sync 等）复用的构造入口。
pub fn make_client_public(cfg: &SyncConfig) -> Result<ApiClient, String> {
    make_client(cfg)
}

fn make_client(cfg: &SyncConfig) -> Result<ApiClient, String> {
    if cfg.server_url.is_empty() {
        return Err("服务器地址未配置，请先在设置页面填写服务器地址".into());
    }
    Ok(ApiClient::new(&cfg.server_url, &cfg.token))
}

// 业务失败统一格式化为 "[code] message"，把后端信封 code 带给前端（前端 String(e) 即可看到码 + 信息）。
fn api_data<T>(resp: api::client::ApiResponse<T>, op: &str) -> Result<T, String> {
    if resp.is_ok() {
        resp.data.ok_or_else(|| format!("[{}] 响应 data 为空", op))
    } else {
        Err(format!("[{}] {}", resp.code, resp.message))
    }
}

// 无数据返回的命令（更新/删除类）统一成功/失败判定，失败带上 code。
fn ensure_ok<T>(resp: api::client::ApiResponse<T>) -> Result<(), String> {
    if resp.is_ok() {
        Ok(())
    } else {
        Err(format!("[{}] {}", resp.code, resp.message))
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

// ── 同步配置 ─────────────────────────────────────────────────────────────────

#[tauri::command]
fn set_sync_config(
    server_url: String,
    ws_url: String,
    token: String,
    upload_workers: Option<usize>,
    download_workers: Option<usize>,
    debounce_ms: Option<u64>,
    log_level: Option<String>,
    config: State<SharedSyncConfig>,
) {
    let mut cfg = config.write();
    cfg.server_url = server_url;
    cfg.ws_url = ws_url;
    cfg.token = token;
    if let Some(w) = upload_workers {
        cfg.upload_workers = w;
    }
    if let Some(w) = download_workers {
        cfg.download_workers = w;
    }
    if let Some(d) = debounce_ms {
        cfg.debounce_ms = d;
    }
    if let Some(lvl) = log_level {
        cfg.log.level = lvl;
    }
    cfg.save(); // 持久化到 config/config.yml（token 不写入）
    logger::info(
        "config",
        format!("同步配置已更新并保存（日志级别: {}）", cfg.log.level),
    );
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
    cfg.token = if cfg.token.is_empty() {
        String::new()
    } else {
        "***".into()
    };
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
        if cfg
            .folder_mappings
            .iter()
            .any(|m| m.local_path == local_path)
        {
            return Err(format!("目录已添加: {}", local_path));
        }
        cfg.folder_mappings.push(FolderMapping {
            local_path: local_path.clone(),
            remote_path,
            folder_id,
        });
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
    config
        .write()
        .folder_mappings
        .retain(|m| m.local_path != local_path);
}

async fn do_start_sync(
    config: &SharedSyncConfig,
    engine: &SharedSyncEngine,
    app_handle: &tauri::AppHandle,
) -> Result<(), String> {
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

    start_sync_engine(engine, config.clone(), app_handle.clone())?;

    let paths: Vec<PathBuf> = config
        .read()
        .folder_mappings
        .iter()
        .map(|m| PathBuf::from(&m.local_path))
        .collect();
    if paths.is_empty() {
        logger::warn(
            "sync",
            "未配置任何同步目录，引擎已启动但不会监听任何文件。请先在「同步管理」创建同步文件夹。",
        );
    }
    for p in &paths {
        match engine_watch_path(engine, p.clone()) {
            Ok(_) => logger::info("sync", format!("开始监听同步目录: {}", p.display())),
            Err(e) => logger::error("sync", format!("监听目录失败 {}: {}", p.display(), e)),
        }
    }

    engine_enqueue_initial_sync(engine, config);
    logger::info(
        "sync",
        format!("同步引擎已启动，监听 {} 个目录", paths.len()),
    );
    Ok(())
}

/// 启动同步引擎：先从服务器拉取 sync_folders 填充内存缓存，再启动引擎和文件监听
#[tauri::command]
async fn start_sync(
    engine: State<'_, SharedSyncEngine>,
    config: State<'_, SharedSyncConfig>,
    app_handle: tauri::AppHandle,
) -> Result<(), String> {
    if engine.lock().is_some() {
        return Ok(()); // 已在运行，幂等
    }
    do_start_sync(&config, &engine, &app_handle).await
}

#[tauri::command]
fn stop_sync(engine: State<SharedSyncEngine>) {
    stop_sync_engine(&engine);
}

#[tauri::command]
fn is_sync_running(engine: State<SharedSyncEngine>) -> bool {
    engine.lock().is_some()
}

/// 查询当前 WS 是否已连接。`ws-status` 事件是边沿信号，前端注册监听后应主动查一次，
/// 补齐可能已错过的连接事件（详见 ws_client::WS_CONNECTED）。
#[tauri::command]
fn is_ws_connected() -> bool {
    ws_client::ws_is_connected()
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
    let resp = user_api::register(
        &client,
        RegisterParams {
            username,
            password,
            email,
        },
    )
    .await?;
    api_data(resp, "register")
}

#[tauri::command]
async fn reset_password(
    username: String,
    old_password: String,
    new_password: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = user_api::reset_password(
        &client,
        ResetPasswordParams {
            username,
            old_password,
            new_password,
        },
    )
    .await?;
    ensure_ok(resp)
}

/// 修改密码（已登录场景，需旧密码验证）
#[tauri::command]
async fn change_password(
    old_password: String,
    new_password: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = user_api::change_password(
        &client,
        ChangePasswordParams {
            old_password,
            new_password,
        },
    )
    .await?;
    ensure_ok(resp)
}

/// 更新用户资料（multipart，可选头像文件）
#[tauri::command]
async fn update_profile(
    username: Option<String>,
    email: Option<String>,
    phone: Option<String>,
    avatar_path: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let mut form = reqwest::multipart::Form::new();
    if let Some(u) = username {
        form = form.text("username", u);
    }
    if let Some(e) = email {
        form = form.text("email", e);
    }
    if let Some(p) = phone {
        form = form.text("phone", p);
    }
    if let Some(path) = avatar_path {
        let p = std::path::Path::new(&path);
        let name = p
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("avatar")
            .to_string();
        let bytes = std::fs::read(p).map_err(|e| format!("读取头像文件失败: {}", e))?;
        let part = reqwest::multipart::Part::bytes(bytes).file_name(name);
        form = form.part("avatar", part);
    }
    let resp = user_api::update_info(&client, form).await?;
    ensure_ok(resp)
}

// ── 文件域 commands ───────────────────────────────────────────────────────────

#[tauri::command]
async fn get_available_disks(
    config: State<'_, SharedSyncConfig>,
) -> Result<AvailableDisksData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::get_available_disks(
        &client,
        AvailableDisksParams {
            disk_path: String::new(),
            detailed: true,
        },
    )
    .await?;
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
    let resp = file_api::traverse_directory(
        &client,
        TraverseDirectoryParams {
            path,
            page,
            page_size,
        },
    )
    .await?;
    api_data(resp, "traverse_directory")
}

/// 上传文件：TS 传本地绝对路径，Rust 走分片上传（blake3 + 乱序并发 + 断点续传 + 秒传）。
/// 进度通过 Tauri 事件 `upload-progress-byte` 推前端（字节级）。
#[tauri::command]
async fn upload_file(
    local_path: String,
    remote_dir: String,
    // on_conflict：目标同名时的策略。None/"reject"=报错；"timestamp"=服务端自动加时间戳区分
    // （发布 APK 用这个：每次 build 出来都叫同一个名字，报错没意义）
    on_conflict: Option<String>,
    config: State<'_, SharedSyncConfig>,
    app_handle: tauri::AppHandle,
) -> Result<UploadCompleteData, String> {
    use chunked_uploader::{upload, ProgressFn, UploadOptions};
    let path = std::path::Path::new(&local_path);
    if !path.exists() {
        return Err(format!("文件不存在: {}", local_path));
    }
    let (client, device_id) = {
        let cfg = config.read();
        (make_client(&cfg)?, cfg.device_id.clone())
    };
    let mut options = UploadOptions::new(device_id);
    if on_conflict.as_deref() == Some("timestamp") {
        options = options.with_timestamp_on_conflict();
    }
    let app_for_progress = app_handle.clone();
    let local_for_progress = local_path.clone();
    let on_progress: ProgressFn = Arc::new(move |sent, total| {
        let _ = app_for_progress.emit(
            "upload-progress-byte",
            serde_json::json!({
                "path": local_for_progress,
                "sent": sent,
                "total": total,
            }),
        );
    });
    let data = upload(&client, path, &remote_dir, &options, on_progress).await?;
    logger::info(
        "upload",
        format!(
            "上传成功: {} ({} bytes, synced={})",
            data.storage_path, data.file_size, data.synced
        ),
    );
    Ok(data)
}

/// 删除远端文件（文件管理用）
#[tauri::command]
async fn delete_file(
    path: String,
    name: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp =
        file_api::delete_file(&client, api::file::params::DeleteFileParams { path, name }).await?;
    ensure_ok(resp)
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
    Ok(file_api::build_download_url(
        &client,
        &DownloadParams {
            path,
            name,
            device_id,
        },
    ))
}

#[tauri::command]
async fn get_download_history(
    page_num: i32,
    page_size: i32,
    config: State<'_, SharedSyncConfig>,
) -> Result<DownloadHistoryData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::get_download_history(
        &client,
        DownloadHistoryParams {
            page_num,
            page_size,
        },
    )
    .await?;
    api_data(resp, "get_download_history")
}

#[tauri::command]
async fn delete_download_history(
    ids: Vec<i64>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp =
        file_api::delete_download_history(&client, DeleteDownloadHistoryParams { ids }).await?;
    ensure_ok(resp)
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
    let params = CreateFolderParams {
        name,
        local_path: local_path.clone(),
        remote_path: remote_path.clone(),
        direction,
        owner_device_id: device_id,
    };
    let resp = sync_api::create_folder(&client, params).await?;
    let folder = api_data(resp, "create_sync_folder")?;

    // 自动更新内存缓存
    {
        let mut cfg = config.write();
        if !cfg
            .folder_mappings
            .iter()
            .any(|m| m.local_path == local_path)
        {
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
    if !resp.is_ok() {
        return Err(format!("[{}] {}", resp.code, resp.message));
    }
    // 从内存缓存移除（停止对该目录的上传调度；watcher 不主动 unwatch，重启后自然消失）
    config
        .write()
        .folder_mappings
        .retain(|m| m.folder_id != folder_id);
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
    ensure_ok(resp)
}

#[tauri::command]
async fn delete_conflict(
    conflict_id: u64,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::delete_conflict(&client, conflict_id).await?;
    ensure_ok(resp)
}

/// 更新同步文件夹（enabled/direction/name，None 字段不动）。
#[tauri::command]
async fn update_sync_folder(
    folder_id: u64,
    enabled: Option<bool>,
    direction: Option<String>,
    name: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::update_folder(
        &client,
        folder_id,
        UpdateFolderParams {
            enabled,
            direction,
            name,
        },
    )
    .await?;
    ensure_ok(resp)
}

/// 分页查询同步任务记录（历史列表）。status 空 = 全部状态。
#[tauri::command]
async fn list_sync_tasks(
    status: Option<String>,
    page: i32,
    page_size: i32,
    config: State<'_, SharedSyncConfig>,
) -> Result<SyncTaskPage, String> {
    let client = make_client(&config.read())?;
    let resp =
        sync_api::list_tasks_paged(&client, status.as_deref().unwrap_or(""), page, page_size)
            .await?;
    api_data(resp, "list_sync_tasks")
}

/// 批量清理终态任务记录（completed/failed），返回删除条数。
#[tauri::command]
async fn clear_sync_tasks(
    status: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<i64, String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::clear_tasks(&client, status.as_deref().unwrap_or("")).await?;
    if resp.is_ok() {
        Ok(resp
            .data
            .and_then(|v| v.get("deleted").and_then(|d| d.as_i64()))
            .unwrap_or(0))
    } else {
        Err(format!("[{}] {}", resp.code, resp.message))
    }
}

// ── 应用更新域 commands ───────────────────────────────────────────────────────
// 走与其它域相同的 invoke → ApiClient 路径（token 来自 SyncConfig，reqwest 直发，
// 避免 webview fetch 的 CORS / localStorage 陈旧 token 导致 401）。用 serde_json::Value
// 透传，无需新增 params/response 结构体。
use api::routes;

#[tauri::command]
async fn list_app_releases(
    platform: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let mut params = std::collections::HashMap::new();
    params.insert("platform", platform);
    let resp: api::client::ApiResponse<serde_json::Value> =
        client.get(routes::UPDATE_RELEASES, Some(&params)).await?;
    api_data(resp, "list_app_releases")
}

/// 通用 API 代理：把任意 v1 接口透传给 Rust 侧的 ApiClient。
///
/// 为什么要有它：Tauri 模式下前端**不能**直接 fetch 后端（webview 里的 token 可能是陈旧的，
/// 且要绕 CORS），既有做法是每个接口写一个 command。管理域一口气新增了二十来个只读/简单写的
/// 接口，逐个包 command 纯属重复劳动，故提供一个统一代理：路径与 body 由前端给，
/// token/服务器地址仍由 Rust 侧的 SyncConfig 提供（安全边界不变）。
///
/// 只允许 /v1 下的相对路径，防止被当成任意 URL 的转发器。
#[tauri::command]
async fn api_request(
    method: String,
    path: String,
    body: Option<serde_json::Value>,
    query: Option<std::collections::HashMap<String, String>>,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    if !path.starts_with('/') || path.contains("://") {
        return Err("非法的接口路径".into());
    }
    let client = make_client(&config.read())?;
    let params: Option<std::collections::HashMap<&str, String>> = query
        .as_ref()
        .map(|m| m.iter().map(|(k, v)| (k.as_str(), v.clone())).collect());

    let resp: api::client::ApiResponse<serde_json::Value> = match method.to_uppercase().as_str() {
        "GET" => client.get(&path, params.as_ref()).await?,
        "POST" => match body {
            Some(b) => client.post(&path, &b).await?,
            None => client.post_empty(&path).await?,
        },
        "PUT" => {
            client
                .put(&path, &body.unwrap_or(serde_json::Value::Null))
                .await?
        }
        "DELETE" => client.delete(&path).await?,
        other => return Err(format!("不支持的方法: {}", other)),
    };
    if !resp.is_ok() {
        return Err(resp.message);
    }
    Ok(resp.data.unwrap_or(serde_json::Value::Null))
}

// ── 剪贴板同步 ───────────────────────────────────────────────────────────────

/// 当前剪贴板同步开关状态。
#[tauri::command]
fn get_clipboard_settings(state: State<clipboard_sync::SharedClipboardState>) -> serde_json::Value {
    let s = state.read();
    serde_json::json!({ "enabled": s.enabled, "auto_apply": s.auto_apply })
}

/// 开关剪贴板同步。**默认关闭**，必须用户显式打开——剪贴板里常有密码和验证码，
/// 内容会经过服务器（虽然只在 Redis 存 24 小时且不进数据库），这一点要在设置页写清楚。
#[tauri::command]
fn set_clipboard_settings(
    enabled: Option<bool>,
    auto_apply: Option<bool>,
    state: State<clipboard_sync::SharedClipboardState>,
) {
    let mut s = state.write();
    if let Some(v) = enabled {
        s.enabled = v;
    }
    if let Some(v) = auto_apply {
        s.auto_apply = v;
    }
    logger::info(
        "clipboard",
        format!(
            "剪贴板同步: enabled={} auto_apply={}",
            s.enabled, s.auto_apply
        ),
    );
}

/// 手动把当前剪贴板内容推送一次（不受 enabled 开关限制，用户点了就是要推）。
#[tauri::command]
async fn push_clipboard_now(
    config: State<'_, SharedSyncConfig>,
    state: State<'_, clipboard_sync::SharedClipboardState>,
) -> Result<(), String> {
    clipboard_sync::push_current(&config, &state).await
}

/// 把一条历史内容写回本机剪贴板（历史列表里点「复制」）。
#[tauri::command]
fn apply_clipboard_text(
    text: String,
    state: State<clipboard_sync::SharedClipboardState>,
) -> Result<(), String> {
    clipboard_sync::apply_to_clipboard(&state, &text)
}

// ── 系统监控（WS 推送）────────────────────────────────────────────────────────

/// 订阅系统监控。进入监控页时调用，指标经 WS 推来，前端监听 `monitor-metrics` 事件。
/// interval 是期望推送间隔（秒），服务端会夹到 [1,10]。断线重连由 Rust 侧自动补订阅。
#[tauri::command]
fn subscribe_monitor(interval: Option<i64>) {
    ws_client::set_monitor_subscription(interval.unwrap_or(2).max(1));
}

/// 退订系统监控。离开监控页时调用，服务端随即停止为本连接采样。
#[tauri::command]
fn unsubscribe_monitor() {
    ws_client::set_monitor_subscription(0);
}

#[tauri::command]
async fn publish_app_release(
    release: serde_json::Value,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let resp: api::client::ApiResponse<serde_json::Value> =
        client.post(routes::UPDATE_PUBLISH, &release).await?;
    api_data(resp, "publish_app_release")
}

#[tauri::command]
async fn update_app_release(
    id: u64,
    updates: serde_json::Value,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let path = routes::UPDATE_RELEASE_BY_ID.replace("{}", &id.to_string());
    let resp: api::client::ApiResponse<serde_json::Value> = client.put(&path, &updates).await?;
    ensure_ok(resp)
}

#[tauri::command]
async fn delete_app_release(id: u64, config: State<'_, SharedSyncConfig>) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let path = routes::UPDATE_RELEASE_BY_ID.replace("{}", &id.to_string());
    let resp: api::client::ApiResponse<serde_json::Value> = client.delete(&path).await?;
    ensure_ok(resp)
}

#[tauri::command]
async fn check_app_update(
    platform: String,
    version_code: i64,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let mut params = std::collections::HashMap::new();
    params.insert("platform", platform);
    params.insert("version_code", version_code.to_string());
    let resp: api::client::ApiResponse<serde_json::Value> =
        client.get(routes::UPDATE_CHECK, Some(&params)).await?;
    api_data(resp, "check_app_update")
}

// ── Tauri 入口 ────────────────────────────────────────────────────────────────

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // 先创建共享配置，setup 与 manage 共用同一 Arc
    let shared_config = init_sync_config();
    let clipboard_state = clipboard_sync::init_clipboard_state();
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(init_watcher_state())
        .manage(shared_config.clone())
        .manage(init_sync_engine())
        .manage(clipboard_state.clone())
        .setup(move |app| {
            // 绑定全局日志（事件 + 文件 + 级别过滤 + 轮转），任意模块即可 logger::info(...)
            let log_cfg = shared_config.read().log.clone();
            logger::init(app.handle().clone(), &log_cfg);
            base_store::init(); // 载入 base_hash 基线
                                // 剪贴板监听：进程内一份，默认关闭（state.enabled=false），用户在设置里打开才真正干活
            clipboard_sync::start_clipboard_watcher(
                clipboard_state.clone(),
                shared_config.clone(),
                app.handle().clone(),
            );
            logger::info(
                "app",
                format!(
                    "应用启动，配置目录: {} | 日志级别: {}",
                    app_paths::config_dir().display(),
                    log_cfg.level
                ),
            );

            // 顶部菜单栏：一级菜单「工具」→「打开日志窗口」(Ctrl+Alt+T)
            use tauri::menu::{MenuBuilder, MenuItemBuilder, SubmenuBuilder};
            let open_logs = MenuItemBuilder::with_id("open_logs", "打开日志窗口")
                .accelerator("CmdOrCtrl+Alt+T")
                .build(app)?;
            let tools = SubmenuBuilder::new(app, "工具").item(&open_logs).build()?;
            let menu = MenuBuilder::new(app).item(&tools).build()?;
            app.set_menu(menu)?;

            // 同步默认启用：延迟 1s 后自动启动（等 app 就绪 + token 可能已加载）
            {
                let cfg = shared_config.clone();
                let eng = app.state::<SharedSyncEngine>().inner().clone();
                let handle = app.handle().clone();
                std::thread::spawn(move || {
                    std::thread::sleep(Duration::from_secs(1));
                    let rt = tokio::runtime::Runtime::new().unwrap();
                    rt.block_on(async {
                        if cfg.read().token.is_empty() || eng.lock().is_some() {
                            return;
                        }
                        if let Err(e) = do_start_sync(&cfg, &eng, &handle).await {
                            logger::warn("app", format!("自动启动同步失败（可手动启动）: {}", e));
                        }
                    });
                });
            }

            Ok(())
        })
        .on_menu_event(|app, event| {
            if event.id().as_ref() == "open_logs" {
                open_log_window(app);
            }
        })
        .invoke_handler(tauri::generate_handler![
            // 基础监听
            add_watch,
            remove_watch,
            list_watches,
            // 配置
            set_sync_config,
            get_sync_config,
            get_device_id,
            // 日志
            open_log_window_cmd,
            // 同步引擎
            add_folder_mapping,
            remove_folder_mapping,
            start_sync,
            stop_sync,
            is_sync_running,
            is_ws_connected,
            // 通用 API 代理（管理域等新接口统一走它）
            api_request,
            // 剪贴板同步
            get_clipboard_settings,
            set_clipboard_settings,
            push_clipboard_now,
            apply_clipboard_text,
            // 系统监控（WS 推送）
            subscribe_monitor,
            unsubscribe_monitor,
            // 用户域
            login,
            verify,
            register,
            reset_password,
            update_profile,
            change_password,
            // 文件域
            get_available_disks,
            traverse_directory,
            upload_file,
            delete_file,
            build_download_url,
            get_download_history,
            delete_download_history,
            // 同步域
            create_sync_folder,
            list_sync_folders,
            delete_sync_folder,
            update_sync_folder,
            list_pending_tasks,
            list_conflicts,
            resolve_conflict,
            delete_conflict,
            list_sync_tasks,
            clear_sync_tasks,
            // 应用更新域
            list_app_releases,
            publish_app_release,
            update_app_release,
            delete_app_release,
            check_app_update,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
