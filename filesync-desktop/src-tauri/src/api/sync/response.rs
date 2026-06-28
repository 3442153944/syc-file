// api/sync/response.rs
// 职责：同步模块所有响应数据结构体，对应 SYNC_PROTOCOL.md §4。
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncFolder {
    pub id: u64,
    pub user_id: u64,
    pub name: String,
    pub local_path: String,
    pub remote_path: String,
    /// two_way / upload_only / download_only
    pub direction: String,
    pub enabled: bool,
    pub owner_device_id: String,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncTask {
    pub id: u64,
    pub folder_id: u64,
    /// download / delete / mkdir
    pub task_type: String,
    /// pending / syncing / completed / failed / conflict / waiting_unlock
    pub sync_status: String,
    pub relative_path: String,
    pub file_name: String,
    pub file_size: Option<i64>,
    pub file_hash: Option<String>,
    pub progress: Option<i32>,
    pub error: Option<String>,
    pub created_at: String,
}

/// 冲突待办记录（GET /sync/conflicts），对应后端 sync_conflict 表。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncConflict {
    pub id: u64,
    pub user_id: u64,
    pub device_id: String,
    pub folder_id: u64,
    pub file_id: u64,
    pub relative_path: String,
    pub file_name: String,
    pub server_hash: Option<String>,
    pub local_hash: Option<String>,
    pub base_hash: Option<String>,
    pub server_version: u32,
    /// pending / resolved
    pub status: String,
    /// accept_server / keep_local
    pub resolution: Option<String>,
    pub created_at: String,
}
