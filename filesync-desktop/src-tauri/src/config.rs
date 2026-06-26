// config.rs
// 职责：SyncConfig 状态定义与 Tauri managed state 初始化。
// 不含业务逻辑，不含 device_id 生成（见 device.rs）。
use crate::device;
use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncConfig {
    pub server_url: String,
    pub ws_url: String,
    pub token: String,
    pub device_id: String,
    pub device_name: String,
    pub folder_mappings: Vec<FolderMapping>,
    pub upload_workers: usize,
    pub debounce_ms: u64,
}

/// 本地目录 ↔ 服务器目录的映射，含 server 侧 folder_id
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FolderMapping {
    pub local_path: String,
    pub remote_path: String,
    /// 服务端 SyncFolder.id，注册后回填
    pub folder_id: u64,
}

impl Default for SyncConfig {
    fn default() -> Self {
        SyncConfig {
            server_url: "http://localhost:8991".into(),
            ws_url: "ws://localhost:8991".into(),
            token: String::new(),
            device_id: device::generate_device_id(),
            device_name: device::hostname(),
            folder_mappings: vec![],
            upload_workers: 2,
            debounce_ms: 500,
        }
    }
}

pub type SharedSyncConfig = Arc<RwLock<SyncConfig>>;

pub fn init_sync_config() -> SharedSyncConfig {
    Arc::new(RwLock::new(SyncConfig::default()))
}
