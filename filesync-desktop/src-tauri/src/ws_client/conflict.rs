// ws_client/conflict.rs
// 职责：处理服务端下发的 conflict / conflict_resolved——隔离本地分叉、收敛服务端版本、
// keep_local 时把隔离副本重新提交为新版本。
use super::download::{build_remote_dir, download_and_publish, now_secs, resolve_local_root, sanitize_rel};
use crate::api::{
    client::ApiClient,
    file::api as file_api,
    file::params::DeleteFileParams,
    sync::api as sync_api,
    sync::params::NotifyParams,
    ws::types::{ConflictContent, ConflictResolvedContent},
};
use crate::base_store;
use crate::chunked_uploader::{self, UploadOptions};
use crate::config::SharedSyncConfig;
use crate::logger;
use parking_lot::Mutex;
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, OnceLock};
use tauri::{AppHandle, Emitter};

/// 待处理冲突：conflict_id → 隔离副本信息，供 keep_local 重新提交。
struct PendingConflict {
    folder_id: u64,
    relative_path: String,
    file_name: String,
    remote_dir: String,
    quarantine: PathBuf,
}

static PENDING: OnceLock<Mutex<HashMap<u64, PendingConflict>>> = OnceLock::new();

fn pending() -> &'static Mutex<HashMap<u64, PendingConflict>> {
    PENDING.get_or_init(|| Mutex::new(HashMap::new()))
}

pub async fn on_conflict(cf: ConflictContent, config: &SharedSyncConfig, app: &AppHandle) {
    let local_root = match resolve_local_root(config, cf.folder_id) {
        Some(r) => r,
        None => return,
    };
    let safe_rel = sanitize_rel(&cf.relative_path);
    let main_path = local_root.join(&safe_rel);

    // 1) 隔离本地分叉到 .syncpending
    let pend_dir = local_root.join(".syncpending");
    tokio::fs::create_dir_all(&pend_dir).await.ok();
    let ts = now_secs();
    let quarantine = pend_dir.join(format!("{}.{}", cf.file_name, ts));
    if main_path.exists() {
        // 防回环：这一步只是把本地分叉挪去隔离区，不是用户删除，不能被 watcher 当真删除上报
        // （不然会把这次冲突处理误传播成"删除"指令派给其它设备）。
        crate::sync_engine::mute_path(&main_path);
        tokio::fs::rename(&main_path, &quarantine).await.ok();
    }

    // 2) 主目录收敛到服务端版本
    let remote_dir = build_remote_dir(config, cf.folder_id, &cf.relative_path);
    let (server_url, token, device_id) = {
        let c = config.read();
        (c.server_url.clone(), c.token.clone(), c.device_id.clone())
    };
    let client = ApiClient::new(&server_url, &token, &device_id);
    match download_and_publish(
        &client,
        remote_dir.clone(),
        &cf.file_name,
        Some(cf.server_hash.as_str()),
        &local_root,
        &safe_rel,
    )
    .await
    {
        Ok(h) => base_store::set_with_file(
            cf.folder_id,
            &cf.relative_path,
            &h,
            &local_root.join(&safe_rel),
        ),
        Err(_) => logger::warn("conflict", "收敛服务端版本失败，将于重连扫描后补齐"),
    }

    // 3) 记待办（供 keep_local 重提交）+ 通知 UI
    if cf.conflict_id != 0 {
        pending().lock().insert(
            cf.conflict_id,
            PendingConflict {
                folder_id: cf.folder_id,
                relative_path: cf.relative_path.clone(),
                file_name: cf.file_name.clone(),
                remote_dir,
                quarantine: quarantine.clone(),
            },
        );
    }
    logger::warn(
        "conflict",
        format!("冲突：已隔离本地副本并收敛服务端版本: {}", cf.relative_path),
    );
    app.emit(
        "sync-conflict",
        serde_json::json!({
            "conflictId": cf.conflict_id,
            "folderId": cf.folder_id,
            "relativePath": cf.relative_path,
            "fileName": cf.file_name,
            "serverHash": cf.server_hash,
            "localHash": cf.local_hash,
            "quarantine": quarantine.to_string_lossy(),
        }),
    )
    .ok();
}

pub async fn on_conflict_resolved(
    cr: ConflictResolvedContent,
    config: &SharedSyncConfig,
    app: &AppHandle,
) {
    match cr.resolution.as_str() {
        "accept_server" => {
            let pc = pending().lock().remove(&cr.conflict_id); // 先取出，确保锁守卫在 await 前释放
            if let Some(pc) = pc {
                tokio::fs::remove_file(&pc.quarantine).await.ok();
            }
            logger::info(
                "conflict",
                format!("冲突 {} 已按服务端版本解决", cr.conflict_id),
            );
        }
        "keep_local" => {
            let pc = pending().lock().remove(&cr.conflict_id);
            match pc {
                Some(pc) => keep_local_reupload(config, pc, &cr.server_hash).await,
                None => logger::warn(
                    "conflict",
                    format!("冲突 {} keep_local 但找不到隔离副本", cr.conflict_id),
                ),
            }
        }
        _ => {}
    }
    app.emit(
        "sync-conflict-resolved",
        serde_json::json!({
            "conflictId": cr.conflict_id, "resolution": cr.resolution
        }),
    )
    .ok();
}

/// keep_local：以 server_hash 为 base，把隔离副本作为新版本上传，并把它放回主目录。
async fn keep_local_reupload(config: &SharedSyncConfig, pc: PendingConflict, server_hash: &str) {
    let (server_url, token, device_id) = {
        let c = config.read();
        (c.server_url.clone(), c.token.clone(), c.device_id.clone())
    };
    let client = ApiClient::new(&server_url, &token, &device_id);

    // 隔离副本必须存在
    if !pc.quarantine.exists() {
        logger::error(
            "conflict",
            format!("keep_local 找不到隔离副本: {}", pc.quarantine.display()),
        );
        return;
    }

    // 1) 同步场景需覆盖：先删远端旧文件（收敛的服务端版本），再分片上传
    let _ = file_api::delete_file(
        &client,
        DeleteFileParams {
            path: pc.remote_dir.clone(),
            name: pc.file_name.clone(),
        },
    )
    .await;

    let options = UploadOptions::new(device_id.clone());
    let on_progress: chunked_uploader::ProgressFn = Arc::new(|_, _| {});
    let complete = match chunked_uploader::upload(
        &client,
        &pc.quarantine,
        &pc.remote_dir,
        &options,
        on_progress,
    )
    .await
    {
        Ok(d) => d,
        Err(e) => {
            logger::error("conflict", format!("keep_local 上传失败: {}", e));
            return;
        }
    };

    let hash = complete.file_hash.clone();
    let size = complete.file_size;

    // 2) 以 server_hash 为 base 上报 file_changed（快进，不再冲突）
    sync_api::notify(
        &client,
        NotifyParams {
            device_id,
            folder_id: pc.folder_id,
            relative_path: pc.relative_path.clone(),
            file_name: pc.file_name.clone(),
            action: "modify".into(),
            file_size: Some(size),
            file_hash: Some(hash.clone()),
            base_hash: Some(server_hash.to_string()),
            is_dir: false,
            mtime: Some(now_secs()),
        },
    )
    .await
    .ok();
    base_store::set_with_file(pc.folder_id, &pc.relative_path, &hash, &pc.quarantine);

    // 3) 把本地版本放回主目录（覆盖刚收敛的服务端版本）
    if let Some(root) = resolve_local_root(config, pc.folder_id) {
        let dest = root.join(sanitize_rel(&pc.relative_path));
        if let Some(parent) = dest.parent() {
            tokio::fs::create_dir_all(parent).await.ok();
        }
        crate::sync_engine::mute_path(&dest); // 防回环：内容跟基线一致，避免又被 watcher 当新变更
        tokio::fs::rename(&pc.quarantine, &dest).await.ok();
    }
    logger::info(
        "conflict",
        format!("冲突已按本地版本解决并重提交: {}", pc.relative_path),
    );
}
