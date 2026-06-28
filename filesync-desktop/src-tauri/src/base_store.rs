// base_store.rs
// 职责：记录每个同步文件 (folder_id, relative_path) 当前已知的服务端 trunk hash，
// 作为下次本地修改上报 file_changed 时的 base_hash（乐观并发 CAS 基线）。
//
// 全局单例 + 持久化到 config/state.json，重启后仍能给出正确 base，避免重启后首次修改被误判冲突。
use crate::app_paths;
use parking_lot::Mutex;
use std::collections::HashMap;
use std::sync::OnceLock;

static STORE: OnceLock<Mutex<HashMap<String, String>>> = OnceLock::new();

fn cell() -> &'static Mutex<HashMap<String, String>> {
    STORE.get_or_init(|| Mutex::new(HashMap::new()))
}

fn key(folder_id: u64, rel: &str) -> String {
    format!("{}:{}", folder_id, rel)
}

/// 启动时从 state.json 载入。
pub fn init() {
    let map: HashMap<String, String> = std::fs::read_to_string(app_paths::state_file())
        .ok()
        .and_then(|t| serde_json::from_str(&t).ok())
        .unwrap_or_default();
    let _ = STORE.set(Mutex::new(map));
}

/// 取某文件已知的服务端 hash（None = 本地无基线，按新文件处理）。
pub fn get(folder_id: u64, rel: &str) -> Option<String> {
    cell().lock().get(&key(folder_id, rel)).cloned()
}

/// 更新基线并持久化（下载完成、上传被接受后调用）。
pub fn set(folder_id: u64, rel: &str, hash: &str) {
    if hash.is_empty() {
        return;
    }
    cell().lock().insert(key(folder_id, rel), hash.to_string());
    persist();
}

/// 删除基线（文件被删除时）。
pub fn remove(folder_id: u64, rel: &str) {
    cell().lock().remove(&key(folder_id, rel));
    persist();
}

fn persist() {
    let snapshot = cell().lock().clone();
    if let Ok(text) = serde_json::to_string(&snapshot) {
        let _ = std::fs::write(app_paths::state_file(), text);
    }
}
