// api/sync/api.rs
// 职责：同步相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 ApiClient，不包含业务逻辑。
use super::{params::*, response::*};
use crate::api::{client::{ApiClient, ApiResponse}, routes};

// ── 文件夹管理 ────────────────────────────────────────────────────────────

pub async fn create_folder(
    client: &ApiClient,
    params: CreateFolderParams,
) -> Result<ApiResponse<SyncFolder>, String> {
    client.post(routes::SYNC_FOLDERS, &params).await
}

pub async fn list_folders(client: &ApiClient) -> Result<ApiResponse<Vec<SyncFolder>>, String> {
    client.get(routes::SYNC_FOLDERS, None).await
}

pub async fn update_folder(
    client: &ApiClient,
    folder_id: u64,
    params: UpdateFolderParams,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_FOLDER_BY_ID.replace("{}", &folder_id.to_string());
    client.put(&path, &params).await
}

pub async fn delete_folder(
    client: &ApiClient,
    folder_id: u64,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_FOLDER_BY_ID.replace("{}", &folder_id.to_string());
    client.delete(&path).await
}

// ── 事件上报（HTTP 回退，WS 不可用时使用） ──────────────────────────────────

pub async fn notify(
    client: &ApiClient,
    params: NotifyParams,
) -> Result<ApiResponse<serde_json::Value>, String> {
    client.post(routes::SYNC_NOTIFY, &params).await
}

pub async fn scan(
    client: &ApiClient,
    report: ScanReport,
) -> Result<ApiResponse<serde_json::Value>, String> {
    client.post(routes::SYNC_SCAN, &report).await
}

// ── 任务管理 ─────────────────────────────────────────────────────────────

pub async fn list_pending_tasks(
    client: &ApiClient,
    device_id: &str,
) -> Result<ApiResponse<Vec<SyncTask>>, String> {
    use std::collections::HashMap;
    let mut params = HashMap::new();
    params.insert("device_id", device_id.to_string());
    client.get(routes::SYNC_TASKS_PENDING, Some(&params)).await
}

pub async fn complete_task(
    client: &ApiClient,
    task_id: u64,
    file_hash: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_TASK_COMPLETE.replace("{}", &task_id.to_string());
    client.post(&path, &TaskCompleteParams { file_hash: file_hash.to_string() }).await
}

pub async fn fail_task(
    client: &ApiClient,
    task_id: u64,
    error: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_TASK_FAILED.replace("{}", &task_id.to_string());
    client.post(&path, &TaskFailedParams { error: error.to_string() }).await
}

/// 目标文件被占用：转 waiting_unlock（不消耗重试次数）。
pub async fn block_task(
    client: &ApiClient,
    task_id: u64,
    reason: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_TASK_BLOCKED.replace("{}", &task_id.to_string());
    client.post(&path, &TaskBlockedParams { reason: reason.to_string() }).await
}

// ── 冲突管理 ─────────────────────────────────────────────────────────────

pub async fn list_conflicts(client: &ApiClient) -> Result<ApiResponse<Vec<SyncConflict>>, String> {
    client.get(routes::SYNC_CONFLICTS, None).await
}

/// 解决冲突：accept_server / keep_local
pub async fn resolve_conflict(
    client: &ApiClient,
    conflict_id: u64,
    resolution: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_CONFLICT_RESOLVE.replace("{}", &conflict_id.to_string());
    client.post(&path, &ResolveConflictParams { resolution: resolution.to_string() }).await
}

pub async fn delete_conflict(
    client: &ApiClient,
    conflict_id: u64,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_CONFLICT_BY_ID.replace("{}", &conflict_id.to_string());
    client.delete(&path).await
}
