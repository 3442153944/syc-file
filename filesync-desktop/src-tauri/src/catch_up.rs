// catch_up.rs
// 职责：WS 重连后追赶离线变更（两阶段），对齐 Android SyncEngine.catchUp / catchUpFolder。
//
// Phase 1：遍历 folder 本地文件，与 base_store（stat 快照）比对：
//   → stat(size/mtime) 变了的重算 blake3，hash ≠ base 则入上传队列（带 base_hash CAS）；
//   → 本地已删且有基线的 → REST notify(delete)。
// Phase 2：发 POST /sync/scan 全量清单，服务端与 trunk 比对后补派 download/delete 任务。
//
// 追赶期间新建立的 WS 连接不会重复触发（AtomicBool 互斥）。
use crate::api::client::ApiClient;
use crate::api::sync::{api as sync_api, params::*};
use crate::base_store;
use crate::chunked_uploader;
use crate::config::SharedSyncConfig;
use crate::logger;
use crate::sync_engine::should_ignore;
use crate::upload_worker::UploadTask;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use tauri::AppHandle;
use tokio::sync::mpsc;

static CATCHING_UP: AtomicBool = AtomicBool::new(false);

/// 对全部 folder_mapping 执行 Phase1+Phase2 追赶。
/// 幂等：正在追赶时会跳过，避免重复并发扫描。
pub async fn catch_up_all_folders(
    config: &SharedSyncConfig,
    upload_tx: &mpsc::Sender<UploadTask>,
    _app: &AppHandle,
) {
    if CATCHING_UP.swap(true, Ordering::SeqCst) {
        return; // 已有追赶在进行
    }

    let (server_url, token, device_id, mappings) = {
        let cfg = config.read();
        (
            cfg.server_url.clone(),
            cfg.token.clone(),
            cfg.device_id.clone(),
            cfg.folder_mappings.clone(),
        )
    };

    if server_url.is_empty() || token.is_empty() || mappings.is_empty() {
        CATCHING_UP.store(false, Ordering::Release);
        return;
    }

    let client = ApiClient::new(&server_url, &token);
    let mut count = 0u64;

    for mapping in &mappings {
        let root = PathBuf::from(&mapping.local_path);
        if !root.exists() {
            continue;
        }
        catch_up_folder(
            &client,
            &device_id,
            upload_tx,
            mapping.folder_id,
            &root,
            &mapping.remote_path,
        )
        .await;
        count += 1;
    }

    CATCHING_UP.store(false, Ordering::Release);
    logger::info("catch_up", format!("追赶完成，处理了 {} 个文件夹", count));
}

async fn catch_up_folder(
    client: &ApiClient,
    device_id: &str,
    upload_tx: &mpsc::Sender<UploadTask>,
    folder_id: u64,
    root: &Path,
    remote_root: &str,
) {
    // 递归遍历
    let mut files: Vec<(String, PathBuf)> = Vec::new();
    let mut dirs: Vec<String> = Vec::new();
    walk_dir(root, "", &mut files, &mut dirs);

    // Phase 1：检测本地变更并上传，检测本地删除并上报
    let present: HashMap<String, bool> = files.iter().map(|(r, _)| (r.clone(), true)).collect();

    for (rel, path) in &files {
        let base = base_store::get(folder_id, rel);

        match base {
            None => {
                // 无基线：新文件，上传 + 上报 create
                let remote_dir = join_remote(remote_root, &dir_of(rel));
                let _ = upload_tx
                    .send(UploadTask {
                        local_path: path.clone(),
                        remote_dir,
                        folder_id,
                        relative_path: rel.clone(),
                        action: "create".into(),
                    })
                    .await;
            }
            Some(base_entry) => {
                // 有基线：比对 stat 快照，未变则跳过（无需重算 hash）
                if let Ok(cur_hash) = chunked_uploader::file_blake3_hex(path) {
                    let (cur_size, cur_mtime) = base_store::stat_file(path);

                    if cur_size == base_entry.size && cur_mtime == base_entry.mtime {
                        // stat 未变：hash 也为 base，仅刷新 stat（以防旧格式 size=0,mtime=0）
                        if cur_hash == base_entry.hash {
                            // 内容一致，无需任何操作
                            continue;
                        }
                        // stat 相等但 hash 不同（罕见：文件被截断写回同大小？）→ 仍要上传
                    }

                    if cur_hash != base_entry.hash {
                        // 内容已变：上传 + 上报 modify，带旧 base_hash 走 CAS
                        let remote_dir = join_remote(remote_root, &dir_of(rel));
                        let _ = upload_tx
                            .send(UploadTask {
                                local_path: path.clone(),
                                remote_dir,
                                folder_id,
                                relative_path: rel.clone(),
                                action: "modify".into(),
                            })
                            .await;
                    } else {
                        // stat 变了但 hash 相同（touch/mtime 漂移）→ 只刷新 stat 快照
                        base_store::set(folder_id, rel, &base_entry.hash, cur_size, cur_mtime);
                    }
                }
            }
        }
    }

    // 本地已删（有基线但文件不在）→ 上报 delete
    for (rel, base_entry) in base_store::folder_snapshot(folder_id) {
        if present.contains_key(&rel) {
            continue;
        }
        let file_name = Path::new(&rel)
            .file_name()
            .map(|n| n.to_string_lossy().to_string())
            .unwrap_or_default();

        sync_api::notify(
            client,
            NotifyParams {
                device_id: device_id.to_string(),
                folder_id,
                relative_path: rel.clone(),
                file_name,
                action: "delete".into(),
                file_size: None,
                file_hash: None,
                base_hash: Some(base_entry.hash.clone()),
                is_dir: false,
                mtime: None,
            },
        )
        .await
        .ok();
        base_store::remove(folder_id, &rel);
        logger::info("catch_up", format!("已上报删除: {}", rel));
    }

    // Phase 2：全量清单交服务端比对（trunk 有本地无 → download；trunk 无本地有 → delete）
    let mut items: Vec<ScanItem> = Vec::with_capacity(files.len() + dirs.len());

    for rel in &dirs {
        let file_name = Path::new(rel)
            .file_name()
            .map(|n| n.to_string_lossy().to_string())
            .unwrap_or_default();
        items.push(ScanItem {
            relative_path: rel.clone(),
            file_name,
            file_size: 0,
            file_hash: String::new(),
            is_dir: true,
            mtime: 0,
        });
    }

    for (rel, path) in &files {
        let file_name = path
            .file_name()
            .map(|n| n.to_string_lossy().to_string())
            .unwrap_or_default();
        let (size, mtime) = base_store::stat_file(path);
        let hash = base_store::get(folder_id, rel)
            .map(|b| b.hash)
            .unwrap_or_else(|| chunked_uploader::file_blake3_hex(path).unwrap_or_default());

        items.push(ScanItem {
            relative_path: rel.clone(),
            file_name,
            file_size: size,
            file_hash: hash,
            is_dir: false,
            mtime,
        });
    }

    let _ = sync_api::scan(
        client,
        ScanReport {
            device_id: device_id.to_string(),
            folder_id,
            items,
        },
    )
    .await;
}

fn walk_dir(
    dir: &Path,
    rel_prefix: &str,
    files: &mut Vec<(String, PathBuf)>,
    dirs: &mut Vec<String>,
) {
    let rd = match std::fs::read_dir(dir) {
        Ok(r) => r,
        Err(_) => return,
    };
    for entry in rd.flatten() {
        let path = entry.path();
        if should_ignore(&path) {
            continue;
        }
        let name = match path.file_name().and_then(|n| n.to_str()) {
            Some(n) => n,
            None => continue,
        };
        let rel = if rel_prefix.is_empty() {
            name.to_string()
        } else {
            format!("{}/{}", rel_prefix, name)
        };

        if path.is_dir() {
            dirs.push(rel.clone());
            walk_dir(&path, &rel, files, dirs);
        } else if path.is_file() {
            files.push((rel, path));
        }
    }
}

fn join_remote(remote_root: &str, rel_dir: &str) -> String {
    let base = remote_root.trim_end_matches('/').trim_end_matches('\\');
    if rel_dir.is_empty() {
        base.to_string()
    } else {
        format!("{}/{}", base, rel_dir)
    }
}

fn dir_of(rel: &str) -> String {
    Path::new(rel)
        .parent()
        .map(|p| p.to_string_lossy().replace('\\', "/"))
        .unwrap_or_default()
}
