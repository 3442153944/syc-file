// base_store.rs
// 职责：记录每个同步文件 (folder_id, relative_path) 当前已知的服务端 trunk hash
// + 本地 stat 快照（size/mtime），作为下次本地修改上报 file_changed 时的
// base_hash（乐观并发 CAS 基线）+ 离线追赶的变更检测优化（stat 未变跳过重算 hash）。
//
// 全局单例 + 持久化到 config/state.json，重启后仍能给出正确 base。
use crate::app_paths;
use parking_lot::Mutex;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::Path;
use std::sync::OnceLock;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BaseEntry {
    pub hash: String,
    pub size: i64,
    pub mtime: i64,
}

static STORE: OnceLock<Mutex<HashMap<String, BaseEntry>>> = OnceLock::new();

fn cell() -> &'static Mutex<HashMap<String, BaseEntry>> {
    STORE.get_or_init(|| Mutex::new(HashMap::new()))
}

fn key(folder_id: u64, rel: &str) -> String {
    format!("{}:{}", folder_id, rel)
}

/// 启动时从 state.json 载入；兼容旧格式（HashMap<String, String>）自动迁移。
pub fn init() {
    let content = std::fs::read_to_string(app_paths::state_file()).ok();
    let map = content
        .and_then(|t| {
            serde_json::from_str::<HashMap<String, BaseEntry>>(&t)
                .ok()
                .or_else(|| {
                    serde_json::from_str::<HashMap<String, String>>(&t)
                        .ok()
                        .map(|old| {
                            old.into_iter()
                                .map(|(k, v)| {
                                    (
                                        k,
                                        BaseEntry {
                                            hash: v,
                                            size: 0,
                                            mtime: 0,
                                        },
                                    )
                                })
                                .collect()
                        })
                })
        })
        .unwrap_or_default();
    let _ = STORE.set(Mutex::new(map));
}

/// 取某文件的基线（hash + stat 快照），None = 本地无基线，按新文件处理。
pub fn get(folder_id: u64, rel: &str) -> Option<BaseEntry> {
    cell().lock().get(&key(folder_id, rel)).cloned()
}

/// 更新基线并持久化（下载完成、上传被接受后调用）。
pub fn set(folder_id: u64, rel: &str, hash: &str, size: i64, mtime: i64) {
    if hash.is_empty() {
        return;
    }
    cell().lock().insert(
        key(folder_id, rel),
        BaseEntry {
            hash: hash.to_string(),
            size,
            mtime,
        },
    );
    persist();
}

/// 便捷方法：从磁盘文件读取 stat 后写入基线。
pub fn set_with_file(folder_id: u64, rel: &str, hash: &str, file_path: &Path) {
    let (size, mtime) = stat_file(file_path);
    set(folder_id, rel, hash, size, mtime);
}

/// 读取文件的 size 与 Unix mtime（秒），取不到返回 (0, 0)。
pub fn stat_file(file_path: &Path) -> (i64, i64) {
    file_path
        .metadata()
        .ok()
        .map(|m| {
            let mt = m
                .modified()
                .ok()
                .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
                .map(|d| d.as_secs() as i64)
                .unwrap_or(0);
            (m.len() as i64, mt)
        })
        .unwrap_or((0, 0))
}

/// 删除基线（文件被删除时）。
pub fn remove(folder_id: u64, rel: &str) {
    cell().lock().remove(&key(folder_id, rel));
    persist();
}

/// 清除某个 folder 的全部基线（映射变更时用）。
pub fn clear_folder(folder_id: u64) {
    let prefix = format!("{}:", folder_id);
    let mut map = cell().lock();
    map.retain(|k, _| !k.starts_with(&prefix));
    drop(map);
    persist();
}

/// 该 folder 的全部基线（追赶扫描比对、检测本地删除用），key 为 relative_path。
pub fn folder_snapshot(folder_id: u64) -> HashMap<String, BaseEntry> {
    let prefix = format!("{}:", folder_id);
    let map = cell().lock();
    map.iter()
        .filter(|(k, _)| k.starts_with(&prefix))
        .map(|(k, v)| (k[prefix.len()..].to_string(), v.clone()))
        .collect()
}

fn persist() {
    let snapshot = cell().lock().clone();
    if let Ok(text) = serde_json::to_string(&snapshot) {
        let _ = std::fs::write(app_paths::state_file(), text);
    }
}
