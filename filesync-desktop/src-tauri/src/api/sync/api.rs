// api/sync/api.rs
// 职责：同步相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 ApiClient，不包含业务逻辑。
use super::{params::*, response::*};
use crate::api::{
    client::{ApiClient, ApiResponse},
    routes,
};

// ── 文件夹管理 ────────────────────────────────────────────────────────────

/// 创建或更新该账号唯一的同步文件夹配置（upsert）。
pub async fn save_folder(
    client: &ApiClient,
    params: SaveFolderParams,
) -> Result<ApiResponse<SyncFolder>, String> {
    client.post(routes::SYNC_FOLDER, &params).await
}

/// 取该账号唯一的同步文件夹配置，未配置时 data 为 null。
pub async fn get_folder(client: &ApiClient) -> Result<ApiResponse<Option<SyncFolder>>, String> {
    client.get(routes::SYNC_FOLDER, None).await
}

pub async fn update_folder(
    client: &ApiClient,
    params: UpdateFolderParams,
) -> Result<ApiResponse<serde_json::Value>, String> {
    client.put(routes::SYNC_FOLDER, &params).await
}

pub async fn delete_folder(client: &ApiClient) -> Result<ApiResponse<serde_json::Value>, String> {
    client.delete(routes::SYNC_FOLDER).await
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

/// 分页查询任务记录（历史列表用；status 空 = 全部状态）。
pub async fn list_tasks_paged(
    client: &ApiClient,
    status: &str,
    page: i32,
    page_size: i32,
) -> Result<ApiResponse<SyncTaskPage>, String> {
    use std::collections::HashMap;
    let mut params = HashMap::new();
    params.insert("page", page.to_string());
    params.insert("page_size", page_size.to_string());
    if !status.is_empty() {
        params.insert("status", status.to_string());
    }
    client.get(routes::SYNC_TASKS, Some(&params)).await
}

/// 批量清理终态任务记录（status 逗号分隔，空 = completed+failed）。
pub async fn clear_tasks(
    client: &ApiClient,
    status: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = if status.is_empty() {
        routes::SYNC_TASKS.to_string()
    } else {
        format!("{}?status={}", routes::SYNC_TASKS, status)
    };
    client.delete(&path).await
}

pub async fn complete_task(
    client: &ApiClient,
    task_id: u64,
    file_hash: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_TASK_COMPLETE.replace("{}", &task_id.to_string());
    client
        .post(
            &path,
            &TaskCompleteParams {
                file_hash: file_hash.to_string(),
            },
        )
        .await
}

pub async fn fail_task(
    client: &ApiClient,
    task_id: u64,
    error: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_TASK_FAILED.replace("{}", &task_id.to_string());
    client
        .post(
            &path,
            &TaskFailedParams {
                error: error.to_string(),
            },
        )
        .await
}

/// 目标文件被占用：转 waiting_unlock（不消耗重试次数）。
pub async fn block_task(
    client: &ApiClient,
    task_id: u64,
    reason: &str,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_TASK_BLOCKED.replace("{}", &task_id.to_string());
    client
        .post(
            &path,
            &TaskBlockedParams {
                reason: reason.to_string(),
            },
        )
        .await
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
    client
        .post(
            &path,
            &ResolveConflictParams {
                resolution: resolution.to_string(),
            },
        )
        .await
}

pub async fn delete_conflict(
    client: &ApiClient,
    conflict_id: u64,
) -> Result<ApiResponse<serde_json::Value>, String> {
    let path = routes::SYNC_CONFLICT_BY_ID.replace("{}", &conflict_id.to_string());
    client.delete(&path).await
}
