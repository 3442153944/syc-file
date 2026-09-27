// ws_client/download.rs
// 职责：下载字节 → 校验 hash → 原子发布到本地文件系统 + 相关路径工具。
// 是 tasks.rs（task_created 的 download 分支）和 conflict.rs（收敛服务端版本）的共用原语。
use crate::api::{client::ApiClient, file::api as file_api, file::params::DownloadParams};
use crate::config::SharedSyncConfig;
use std::path::{Component, PathBuf};

/// 原子发布的错误分类：Locked = 目标被占用（转 waiting_unlock），Other = 真失败。
pub enum PublishErr {
    Locked,
    Other(String),
}

/// 下载字节 → 校验 hash → 写 .synctmp → 原子 rename 到主目录，返回落盘 hash。
pub async fn download_and_publish(
    client: &ApiClient,
    remote_dir: String,
    file_name: &str,
    expected_hash: Option<&str>,
    local_root: &PathBuf,
    safe_rel: &PathBuf,
) -> Result<String, PublishErr> {
    let url = file_api::build_download_url(
        client,
        &DownloadParams {
            path: remote_dir,
            name: file_name.to_string(),
            device_id: String::new(),
        },
    );

    let resp = reqwest::get(&url)
        .await
        .map_err(|e| PublishErr::Other(e.to_string()))?;
    if !resp.status().is_success() {
        return Err(PublishErr::Other(format!("HTTP {}", resp.status())));
    }
    let bytes = resp
        .bytes()
        .await
        .map_err(|e| PublishErr::Other(e.to_string()))?;

    // blake3 校验：算 blake3 hex 与 expected_hash 比对，不匹配直接失败。
    // expected_hash 为空字符串（trunk 尚未知道该文件 hash，比如刚被收编的远端已有文件）
    // 时不校验、直接采信下载内容——远端目录是权威源，此时没有可比对的基准。
    let actual = blake3_hex(&bytes);
    if let Some(exp) = expected_hash.filter(|s| !s.is_empty()) {
        if exp != actual {
            return Err(PublishErr::Other(format!(
                "hash 不匹配 expected={} actual={}",
                exp, actual
            )));
        }
    }

    // 写临时文件（.synctmp 已被 watcher 忽略）
    let tmp_dir = local_root.join(".synctmp");
    tokio::fs::create_dir_all(&tmp_dir).await.ok();
    let tmp_path = tmp_dir.join(format!("{}.{}.tmp", file_name, uuid::Uuid::new_v4()));
    tokio::fs::write(&tmp_path, &bytes)
        .await
        .map_err(|e| PublishErr::Other(e.to_string()))?;

    // 原子 rename 到主目录
    let final_path = local_root.join(safe_rel);
    if let Some(parent) = final_path.parent() {
        tokio::fs::create_dir_all(parent).await.ok();
    }
    crate::sync_engine::mute_path(&final_path); // 防回环：这是引擎自己落盘的内容，不是用户手改
    match tokio::fs::rename(&tmp_path, &final_path).await {
        Ok(_) => Ok(actual),
        Err(e) => {
            tokio::fs::remove_file(&tmp_path).await.ok();
            // Windows: 32=共享冲突(被占用) 5=拒绝访问
            match e.raw_os_error() {
                Some(32) | Some(5) => Err(PublishErr::Locked),
                _ => Err(PublishErr::Other(e.to_string())),
            }
        }
    }
}

// ── 路径工具 ─────────────────────────────────────────────────────────────────

pub fn resolve_local_root(config: &SharedSyncConfig, folder_id: u64) -> Option<PathBuf> {
    let cfg = config.read();
    cfg.folder_mappings
        .iter()
        .find(|m| m.folder_id == folder_id)
        .map(|m| PathBuf::from(&m.local_path))
}

pub fn resolve_local_file(
    config: &SharedSyncConfig,
    folder_id: u64,
    relative_path: &str,
) -> Option<PathBuf> {
    let root = resolve_local_root(config, folder_id)?;
    Some(root.join(sanitize_rel(relative_path)))
}

/// 只保留普通路径段，丢弃 `..`/根，防止路径穿越。
pub fn sanitize_rel(relative_path: &str) -> PathBuf {
    PathBuf::from(relative_path)
        .components()
        .filter(|c| matches!(c, Component::Normal(_)))
        .collect()
}

/// 由 folder 映射 + 相对路径拼出服务端远端目录（用于下载 URL 的 path）。
pub fn build_remote_dir(config: &SharedSyncConfig, folder_id: u64, rel: &str) -> String {
    let cfg = config.read();
    if let Some(m) = cfg
        .folder_mappings
        .iter()
        .find(|m| m.folder_id == folder_id)
    {
        let rel_dir = PathBuf::from(rel)
            .parent()
            .map(|p| p.to_string_lossy().replace('\\', "/"))
            .unwrap_or_default();
        let base = m.remote_path.trim_end_matches('/');
        if rel_dir.is_empty() {
            base.to_string()
        } else {
            format!("{}/{}", base, rel_dir)
        }
    } else {
        String::new()
    }
}

// ── 工具函数 ─────────────────────────────────────────────────────────────────

pub fn blake3_hex(bytes: &[u8]) -> String {
    blake3::hash(bytes).to_hex().to_string()
}

pub fn now_secs() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}
