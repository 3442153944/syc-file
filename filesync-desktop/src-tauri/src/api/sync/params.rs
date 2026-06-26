// api/sync/params.rs
// 职责：同步模块所有请求参数结构体，严格对应 SYNC_PROTOCOL.md §3/§4。
use serde::{Deserialize, Serialize};

// ── 文件夹注册 ────────────────────────────────────────────────────────────

#[derive(Debug, Serialize, Deserialize)]
pub struct CreateFolderParams {
    pub name: String,
    pub local_path: String,
    pub remote_path: String,
    /// two_way / upload_only / download_only
    pub direction: String,
    pub owner_device_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct UpdateFolderParams {
    pub enabled: Option<bool>,
    pub direction: Option<String>,
    pub name: Option<String>,
}

// ── file_changed 上报（§3.1.1） ────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FileChangeReport {
    pub folder_id: u64,
    pub relative_path: String,
    pub file_name: String,
    /// create / modify / delete
    pub action: String,
    pub file_size: Option<i64>,
    pub file_hash: Option<String>,
    pub is_dir: bool,
    pub mtime: Option<i64>,
}

// ── scan_result 全量清单（§3.1.2） ─────────────────────────────────────────

#[derive(Debug, Serialize, Deserialize)]
pub struct ScanItem {
    pub relative_path: String,
    pub file_name: String,
    pub file_size: i64,
    pub file_hash: String,
    pub is_dir: bool,
    pub mtime: i64,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ScanReport {
    pub folder_id: u64,
    pub items: Vec<ScanItem>,
}

/// HTTP 回退版 notify（/sync/notify），字段同 FileChangeReport + device_id
#[derive(Debug, Serialize, Deserialize)]
pub struct NotifyParams {
    pub device_id: String,
    pub folder_id: u64,
    pub relative_path: String,
    pub file_name: String,
    pub action: String,
    pub file_size: Option<i64>,
    pub file_hash: Option<String>,
    pub is_dir: bool,
    pub mtime: Option<i64>,
}

/// 任务完成回调（§3.1.4）
#[derive(Debug, Serialize, Deserialize)]
pub struct TaskCompleteParams {
    pub file_hash: String,
}

/// 任务失败回调（§3.1.5）
#[derive(Debug, Serialize, Deserialize)]
pub struct TaskFailedParams {
    pub error: String,
}
