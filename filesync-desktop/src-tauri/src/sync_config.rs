use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::sync::Arc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncConfig {
    pub server_url: String,
    pub ws_url: String,
    pub token: String,
    /// 机器唯一标识（sha256[:16]），WS 连接时带上，用于任务定向派发
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
            server_url: String::new(),
            ws_url: String::new(),
            token: String::new(),
            device_id: generate_device_id(),
            device_name: hostname(),
            folder_mappings: vec![],
            upload_workers: 2,
            debounce_ms: 500,
        }
    }
}

/// 生成稳定设备 ID：机器名 + 用户名 → sha256 前 16 字符
pub fn generate_device_id() -> String {
    let raw = format!("{}-{}", hostname(), username());
    let hash = Sha256::digest(raw.as_bytes());
    hex::encode(&hash[..8]) // 16 hex chars
}

fn hostname() -> String {
    std::env::var("COMPUTERNAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .unwrap_or_else(|_| "unknown-host".into())
}

fn username() -> String {
    std::env::var("USERNAME")
        .or_else(|_| std::env::var("USER"))
        .unwrap_or_else(|_| "unknown-user".into())
}

pub type SharedSyncConfig = Arc<RwLock<SyncConfig>>;

pub fn init_sync_config() -> SharedSyncConfig {
    Arc::new(RwLock::new(SyncConfig::default()))
}
