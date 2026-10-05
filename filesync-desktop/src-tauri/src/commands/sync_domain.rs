// commands/sync_domain.rs
// 职责：同步域——服务端 sync_folder 的增删改查、待办任务/冲突列表、任务历史。
// 和 sync_engine.rs（本地文件监听/上传调度引擎）是两回事：这里全是对服务端 API 的直接透传。
use crate::api::sync::{api as sync_api, params::*, response::*};
use crate::commands::support::{api_data, ensure_ok, make_client};
use crate::config::{FolderMapping, SharedSyncConfig};
use crate::sync_engine::{engine_watch_path, SharedSyncEngine};
use std::path::PathBuf;
use tauri::State;

/// 创建或更新该账号唯一的同步文件夹配置（upsert）。
#[tauri::command]
pub async fn save_sync_folder(
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
    let params = SaveFolderParams {
        name,
        local_path: local_path.clone(),
        remote_path: remote_path.clone(),
        direction,
        owner_device_id: device_id,
    };
    let resp = sync_api::save_folder(&client, params).await?;
    let folder = api_data(resp, "save_sync_folder")?;

    // 本地目录只记在本机（服务端那份仅作参考，不被其它终端采用），
    // 同时覆盖式更新内存缓存：全局只保留这一条映射
    {
        let mut cfg = config.write();
        cfg.set_local_path(folder.id, &local_path);
        cfg.folder_mappings = vec![FolderMapping {
            local_path: local_path.clone(),
            remote_path,
            folder_id: folder.id,
        }];
    }
    // 如果引擎已在运行，立即开始监听新目录
    if engine.lock().is_some() {
        engine_watch_path(&engine, PathBuf::from(&local_path)).ok();
    }

    Ok(folder)
}

/// 取该账号唯一的同步文件夹配置，未配置时返回 null。
/// 返回值里的 local_path 是「本机」的目录；本机还没设置时为空串（服务端存的是别的终端的路径）。
#[tauri::command]
pub async fn get_sync_folder(config: State<'_, SharedSyncConfig>) -> Result<Option<SyncFolder>, String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::get_folder(&client).await?;
    let folder: Option<SyncFolder> = api_data(resp, "get_sync_folder")?;
    Ok(folder.map(|mut f| {
        f.local_path = config
            .write()
            .resolve_local_path(f.id, &f.owner_device_id, &f.local_path)
            .unwrap_or_default();
        f
    }))
}

#[tauri::command]
pub async fn delete_sync_folder(config: State<'_, SharedSyncConfig>) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::delete_folder(&client).await?;
    if !resp.is_ok() {
        return Err(format!("[{}] {}", resp.code, resp.message));
    }
    // 清空内存缓存（停止对该目录的上传调度；watcher 不主动 unwatch，重启后自然消失）
    config.write().folder_mappings.clear();
    Ok(())
}

#[tauri::command]
pub async fn list_pending_tasks(config: State<'_, SharedSyncConfig>) -> Result<Vec<SyncTask>, String> {
    let (device_id, client) = {
        let cfg = config.read();
        (cfg.device_id.clone(), make_client(&cfg)?)
    };
    let resp = sync_api::list_pending_tasks(&client, &device_id).await?;
    api_data(resp, "list_pending_tasks")
}

#[tauri::command]
pub async fn list_conflicts(config: State<'_, SharedSyncConfig>) -> Result<Vec<SyncConflict>, String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::list_conflicts(&client).await?;
    api_data(resp, "list_conflicts")
}

/// 解决冲突：resolution = accept_server / keep_local
#[tauri::command]
pub async fn resolve_conflict(
    conflict_id: u64,
    resolution: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::resolve_conflict(&client, conflict_id, &resolution).await?;
    ensure_ok(resp)
}

#[tauri::command]
pub async fn delete_conflict(
    conflict_id: u64,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::delete_conflict(&client, conflict_id).await?;
    ensure_ok(resp)
}

/// 更新该账号唯一的同步文件夹配置（enabled/direction/name，None 字段不动）。
#[tauri::command]
pub async fn update_sync_folder(
    enabled: Option<bool>,
    direction: Option<String>,
    name: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = sync_api::update_folder(
        &client,
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
pub async fn list_sync_tasks(
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
pub async fn clear_sync_tasks(
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
