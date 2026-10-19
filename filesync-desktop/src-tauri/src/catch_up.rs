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

/// 一次追赶的实绩：检查了多少文件夹/文件，真正入队/上报了多少，以及服务端清单是否比对成功。
#[derive(Default)]
struct FolderStats {
    files: usize,
    dirs: usize,
    uploads: usize,
    deletes: usize,
    skipped: usize,
    scan_ok: bool,
}

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

    let client = ApiClient::new(&server_url, &token, &device_id);
    let mut total = FolderStats {
        scan_ok: true,
        ..Default::default()
    };
    let mut folders = 0usize;

    for mapping in &mappings {
        let root = PathBuf::from(&mapping.local_path);
        if !root.exists() {
            continue;
        }
        let stats = catch_up_folder(
            &client,
            &device_id,
            upload_tx,
            mapping.folder_id,
            &root,
            &mapping.remote_path,
        )
        .await;
        folders += 1;
        total.files += stats.files;
        total.dirs += stats.dirs;
        total.uploads += stats.uploads;
        total.deletes += stats.deletes;
        total.skipped += stats.skipped;
        total.scan_ok &= stats.scan_ok;
    }

    CATCHING_UP.store(false, Ordering::Release);
    if folders == 0 {
        logger::info("catch_up", "追赶完成: 没有可检查的文件夹");
        return;
    }
    let scan = if total.scan_ok {
        "服务端清单已比对"
    } else {
        "服务端清单比对失败"
    };
    if total.uploads + total.deletes == 0 {
        logger::info(
            "catch_up",
            format!(
                "追赶完成: 检查 {} 个文件夹、{} 个文件（{} 个目录），无本地变更（{}）",
                folders, total.files, total.dirs, scan
            ),
        );
    } else {
        logger::info(
            "catch_up",
            format!(
                "追赶完成: 检查 {} 个文件夹、{} 个文件（{} 个目录），上传入队 {}，删除 {}，未变 {}（{}）",
                folders, total.files, total.dirs, total.uploads, total.deletes, total.skipped, scan
            ),
        );
    }
}

async fn catch_up_folder(
    client: &ApiClient,
    device_id: &str,
    upload_tx: &mpsc::Sender<UploadTask>,
    folder_id: u64,
    root: &Path,
    remote_root: &str,
) -> FolderStats {
    // 递归遍历
    let mut files: Vec<(String, PathBuf)> = Vec::new();
    let mut dirs: Vec<String> = Vec::new();
    walk_dir(root, "", &mut files, &mut dirs);
    let mut stats = FolderStats {
        files: files.len(),
        dirs: dirs.len(),
        ..Default::default()
    };

    // Phase 1：检测本地变更并上传，检测本地删除并上报
    let present: HashMap<String, bool> = files.iter().map(|(r, _)| (r.clone(), true)).collect();

    for (rel, path) in &files {
        let base = base_store::get(folder_id, rel);

        match base {
            None => {
                // 无基线：新文件，上传 + 上报 create
                let remote_dir = join_remote(remote_root, &dir_of(rel));
                if upload_tx
                    .send(UploadTask {
                        local_path: path.clone(),
                        remote_dir,
                        folder_id,
                        relative_path: rel.clone(),
                        action: "create".into(),
                    })
                    .await
                    .is_ok()
                {
                    stats.uploads += 1;
                }
            }
            Some(base_entry) => {
                let (cur_size, cur_mtime) = base_store::stat_file(path);
                // stat 未变就先信任基线，不重算 blake3：否则每次 WS 重连都会把整个同步目录重读一遍。
                if cur_size == base_entry.size && cur_mtime == base_entry.mtime {
                    stats.skipped += 1;
                    continue;
                }
                let Ok(cur_hash) = chunked_uploader::file_blake3_hex(path) else {
                    continue;
                };
                if cur_hash != base_entry.hash {
                    // 内容已变：上传 + 上报 modify，带旧 base_hash 走 CAS
                    let remote_dir = join_remote(remote_root, &dir_of(rel));
                    if upload_tx
                        .send(UploadTask {
                            local_path: path.clone(),
                            remote_dir,
                            folder_id,
                            relative_path: rel.clone(),
                            action: "modify".into(),
                        })
                        .await
                        .is_ok()
                    {
                        stats.uploads += 1;
                    }
                } else {
                    // stat 变了但 hash 相同（touch/mtime 漂移）→ 只刷新 stat 快照
                    base_store::set(folder_id, rel, &base_entry.hash, cur_size, cur_mtime);
                    stats.skipped += 1;
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

        match sync_api::notify(
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
        {
            Ok(r) if r.is_ok() => {
                base_store::remove(folder_id, &rel);
                stats.deletes += 1;
                logger::info("catch_up", format!("已上报删除: {}", rel));
            }
            Ok(r) => logger::warn(
                "catch_up",
                format!("上报删除失败: {} [{}] {}", rel, r.code, r.message),
            ),
            Err(e) => logger::warn("catch_up", format!("上报删除失败: {}: {}", rel, e)),
        }
    }

    // Phase 2：全量清单交服务端比对（trunk 有本地无/不一致 → download；trunk 已软删本地还有 → delete）。
    // 无基线文件照常上报当前真实 hash：服务端对从未登记过的路径不派 delete（等上传/notify 登记），
    // 对已在 trunk 的同 hash 文件也不会补派 download——不能像以前那样把它们从清单里剔除，
    // 否则服务端会按「trunk 有、本地无」给每个文件补派下载，造成同 hash 全量重下。
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

    stats.scan_ok = match sync_api::scan(
        client,
        ScanReport {
            device_id: device_id.to_string(),
            folder_id,
            items,
        },
    )
    .await
    {
        Ok(r) => r.is_ok(),
        Err(_) => false,
    };
    stats
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
