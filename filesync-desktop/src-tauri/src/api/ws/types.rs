// api/ws/types.rs
// 职责：WebSocket 消息信封及各 event 的 content 结构体，严格对应 SYNC_PROTOCOL.md §2/§3。
use serde::{Deserialize, Serialize};

/// WS 消息信封：{"id":"...","type":"file_sync","content":{...},"timestamp":...}
#[derive(Debug, Deserialize)]
pub struct WsEnvelope {
    #[serde(rename = "type")]
    pub kind: String,
    pub content: Option<serde_json::Value>,
}

/// S→C task_created content（§3.2.1）
#[derive(Debug, Deserialize)]
pub struct TaskCreatedContent {
    pub event: String,
    pub task_id: u64,
    pub task_type: String,
    pub direction: Option<String>,
    pub folder_id: u64,
    pub relative_path: String,
    pub file_name: String,
    pub file_size: Option<i64>,
    pub file_hash: Option<String>,
    pub remote_path: Option<String>,
    pub remote_dir: Option<String>,
}

/// S→C conflict content（§3.2.2）：要求源设备隔离本地副本并收敛到 server_hash
#[derive(Debug, Deserialize)]
pub struct ConflictContent {
    #[serde(default)]
    pub conflict_id: u64,
    pub folder_id: u64,
    pub relative_path: String,
    pub file_name: String,
    pub server_hash: String,
    #[serde(default)]
    pub server_version: u32,
    #[serde(default)]
    pub base_hash: Option<String>,
    pub local_hash: String,
}

/// S→C conflict_resolved content（§3.2.3）：待办处理结果回执
#[derive(Debug, Deserialize)]
pub struct ConflictResolvedContent {
    pub conflict_id: u64,
    /// accept_server / keep_local
    pub resolution: String,
    #[serde(default)]
    pub server_hash: String,
}

/// C→S file_changed content（§3.1.1），由 sync engine 构造后走 WS 发送
#[derive(Debug, Clone, Serialize)]
pub struct FileChangedContent {
    pub event: &'static str,
    pub folder_id: u64,
    pub relative_path: String,
    pub file_name: String,
    pub action: String,
    pub file_size: Option<i64>,
    pub file_hash: Option<String>,
    pub base_hash: Option<String>,
    pub is_dir: bool,
    pub mtime: Option<i64>,
}

/// C→S task_completed content（§3.1.4）
#[derive(Debug, Serialize)]
pub struct TaskCompletedContent {
    pub event: &'static str,
    pub task_id: u64,
    pub file_hash: String,
}

/// C→S task_failed content（§3.1.5）
#[derive(Debug, Serialize)]
pub struct TaskFailedContent {
    pub event: &'static str,
    pub task_id: u64,
    pub error: String,
}

/// C→S scan_result content（§3.1.2）
#[derive(Debug, Serialize)]
pub struct ScanResultContent<'a> {
    pub event: &'static str,
    pub folder_id: u64,
    pub items: &'a [crate::api::sync::params::ScanItem],
}
