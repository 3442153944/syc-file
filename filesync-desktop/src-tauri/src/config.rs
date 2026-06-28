// config.rs
// 职责：SyncConfig 运行期状态 + config/config.yml 的加载与持久化。
// 基准目录见 app_paths.rs。token 不落 yml（避免明文凭证），仅运行期/前端 localStorage 持有。
use crate::app_paths;
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
    pub download_workers: usize,
    pub debounce_ms: u64,
    /// 默认同步根目录（base/sync）
    pub sync_root: String,
}

/// 本地目录 ↔ 服务器目录的映射，含 server 侧 folder_id
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FolderMapping {
    pub local_path: String,
    pub remote_path: String,
    /// 服务端 SyncFolder.id，注册后回填
    pub folder_id: u64,
}

/// config.yml 持久化字段（不含 token / device_id 等运行期值）。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FileConfig {
    pub server_url: String,
    pub ws_url: String,
    pub upload_workers: usize,
    pub download_workers: usize,
    pub debounce_ms: u64,
    pub sync_root: String,
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
            upload_workers: 4,
            download_workers: 4,
            debounce_ms: 800,
            sync_root: app_paths::sync_dir().to_string_lossy().to_string(),
        }
    }
}

impl SyncConfig {
    /// 从 config.yml 加载（缺失则写入默认值），device_id/device_name 始终运行期生成。
    pub fn load() -> Self {
        let mut cfg = SyncConfig::default();
        match std::fs::read_to_string(app_paths::config_file()) {
            Ok(text) => match serde_yaml::from_str::<FileConfig>(&text) {
                Ok(fc) => cfg.apply_file(fc),
                Err(_) => cfg.save(), // 解析失败则用默认值覆盖回写
            },
            Err(_) => cfg.save(), // 首次运行：落默认 config.yml
        }
        cfg
    }

    /// 把当前配置的持久化子集写回 config.yml。
    pub fn save(&self) {
        let fc = FileConfig {
            server_url: self.server_url.clone(),
            ws_url: self.ws_url.clone(),
            upload_workers: self.upload_workers,
            download_workers: self.download_workers,
            debounce_ms: self.debounce_ms,
            sync_root: self.sync_root.clone(),
        };
        if let Ok(text) = serde_yaml::to_string(&fc) {
            let _ = std::fs::write(app_paths::config_file(), text);
        }
    }

    fn apply_file(&mut self, fc: FileConfig) {
        self.server_url = fc.server_url;
        self.ws_url = fc.ws_url;
        if fc.upload_workers > 0 {
            self.upload_workers = fc.upload_workers;
        }
        if fc.download_workers > 0 {
            self.download_workers = fc.download_workers;
        }
        if fc.debounce_ms > 0 {
            self.debounce_ms = fc.debounce_ms;
        }
        if !fc.sync_root.is_empty() {
            self.sync_root = fc.sync_root;
        }
    }
}

pub type SharedSyncConfig = Arc<RwLock<SyncConfig>>;

pub fn init_sync_config() -> SharedSyncConfig {
    Arc::new(RwLock::new(SyncConfig::load()))
}
