// ws_client/tasks.rs
// 职责：处理服务端下发的 task_created（download / delete / mkdir）。
use super::download::{download_and_publish, resolve_local_file, resolve_local_root, sanitize_rel, PublishErr};
use crate::api::{client::ApiClient, sync::api as sync_api, ws::types::TaskCreatedContent};
use crate::base_store;
use crate::config::SharedSyncConfig;
use crate::logger;
use parking_lot::Mutex;
use std::collections::HashSet;
use std::sync::{Arc, OnceLock};
use tokio::sync::Semaphore;

/// 下载并发信号量（首次全量同步、多小文件时限流，避免打爆磁盘/网络）。
static DL_SEM: OnceLock<Arc<Semaphore>> = OnceLock::new();

fn dl_sem(config: &SharedSyncConfig) -> Arc<Semaphore> {
    DL_SEM
        .get_or_init(|| {
            let n = config.read().download_workers.max(1);
            Arc::new(Semaphore::new(n))
        })
        .clone()
}

/// 正在处理的任务 id。同一个任务可能被下发多次：WS 推送 + 降级轮询拉取、服务端超时重派……
/// 没有这道去重，一个大文件下载到一半又被派一次，就会两路同时写同一个目标文件。
static IN_FLIGHT: OnceLock<Mutex<HashSet<u64>>> = OnceLock::new();

fn in_flight() -> &'static Mutex<HashSet<u64>> {
    IN_FLIGHT.get_or_init(|| Mutex::new(HashSet::new()))
}

/// 持有期间该任务算「处理中」，drop 时自动登出（download 分支把它带进后台任务，跑完才释放）。
struct TaskGuard(Option<u64>);

impl TaskGuard {
    /// 已在处理中返回 None。task_id 为 0 视为无效 id，不参与去重。
    fn acquire(task_id: u64) -> Option<Self> {
        if task_id == 0 {
            return Some(TaskGuard(None));
        }
        in_flight().lock().insert(task_id).then_some(TaskGuard(Some(task_id)))
    }
}

impl Drop for TaskGuard {
    fn drop(&mut self) {
        if let Some(id) = self.0 {
            in_flight().lock().remove(&id);
        }
    }
}

pub async fn on_task_created(tc: TaskCreatedContent, config: &SharedSyncConfig) {
    let Some(guard) = TaskGuard::acquire(tc.task_id) else {
        logger::debug("task", format!("任务 {} 正在处理，忽略重复下发", tc.task_id));
        return;
    };
    let (server_url, token, device_id) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone(), cfg.device_id.clone())
    };
    let client = ApiClient::new(&server_url, &token, &device_id);

    match tc.task_type.as_str() {
        "download" => {
            let remote_dir = tc.remote_dir.clone().unwrap_or_default();
            logger::info("task", format!("收到下载任务: {}", tc.relative_path));
            tokio::spawn(do_download(
                tc.task_id,
                client,
                config.clone(),
                remote_dir,
                tc.folder_id,
                tc.relative_path,
                tc.file_name,
                tc.file_hash,
                guard,
            ));
        }
        "delete" => {
            let path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地文件")
                        .await
                        .ok();
                    return;
                }
            };
            crate::sync_engine::mute_path(&path); // 防回环：这是引擎自己执行的删除，不是用户手删
            tokio::fs::remove_file(&path).await.ok(); // 不存在也视为成功
            base_store::remove(tc.folder_id, &tc.relative_path);
            sync_api::complete_task(&client, tc.task_id, "").await.ok();
            logger::info("task", format!("已删除本地文件: {}", tc.relative_path));
            crate::transfers::sync_event(&path.to_string_lossy(), "deleted_by_server");
        }
        "mkdir" => {
            let path = match resolve_local_file(config, tc.folder_id, &tc.relative_path) {
                Some(p) => p,
                None => {
                    sync_api::fail_task(&client, tc.task_id, "未找到本地路径")
                        .await
                        .ok();
                    return;
                }
            };
            match tokio::fs::create_dir_all(&path).await {
                Ok(_) => {
                    sync_api::complete_task(&client, tc.task_id, "").await.ok();
                    logger::info("task", format!("已创建本地目录: {}", tc.relative_path));
                }
                Err(e) => {
                    sync_api::fail_task(&client, tc.task_id, &e.to_string())
                        .await
                        .ok();
                }
            }
        }
        _ => {}
    }
}

#[allow(clippy::too_many_arguments)]
async fn do_download(
    task_id: u64,
    client: ApiClient,
    config: SharedSyncConfig,
    remote_dir: String,
    folder_id: u64,
    relative_path: String,
    file_name: String,
    expected_hash: Option<String>,
    _guard: TaskGuard,
) {
    let _permit = dl_sem(&config).acquire_owned().await.ok(); // 并发限流

    let local_root = match resolve_local_root(&config, folder_id) {
        Some(r) => r,
        None => {
            sync_api::fail_task(&client, task_id, "未找到本地目录映射")
                .await
                .ok();
            return;
        }
    };
    let safe_rel = sanitize_rel(&relative_path);
    let final_str = local_root.join(&safe_rel).to_string_lossy().to_string();

    // 本地有尚未同步的编辑（内容既不是基线、也不是要下载的版本）：不能覆盖，否则离线编辑直接丢了。
    // 交给上传侧的版本检查裁决：基于最新版本的修改快进成为新版本；与其它设备分叉则转入冲突待办，
    // 本地副本隔离到 .syncpending 让用户选保留哪个。
    if let Some(exp) = expected_hash.as_deref().filter(|h| !h.is_empty()) {
        let local = local_root.join(&safe_rel);
        if local.is_file() {
            if let Ok(cur) = crate::chunked_uploader::file_blake3_hex(&local) {
                // 本地内容与任务一致：无需重新下载（服务端补派/追赶误派时最常见），
                // 直接刷新基线并完成任务。
                if cur == exp {
                    sync_api::complete_task(&client, task_id, &cur).await.ok();
                    base_store::set_with_file(folder_id, &relative_path, &cur, &local);
                    logger::info(
                        "download",
                        format!("本地已是最新，跳过下载: {}", relative_path),
                    );
                    crate::transfers::download_progress(&final_str, "done", Some(task_id), None);
                    return;
                }
                let base = base_store::get(folder_id, &relative_path).map(|b| b.hash);
                if base.as_deref() != Some(cur.as_str()) {
                    logger::warn(
                        "download",
                        format!("本地有未同步的修改，跳过下载以免覆盖: {}", relative_path),
                    );
                    sync_api::complete_task(&client, task_id, "").await.ok();
                    return;
                }
            }
        }
    }

    crate::transfers::download_progress(&final_str, "downloading", Some(task_id), None);

    match download_and_publish(
        &client,
        remote_dir,
        &file_name,
        expected_hash.as_deref(),
        &local_root,
        &safe_rel,
    )
    .await
    {
        Ok(hash) => {
            sync_api::complete_task(&client, task_id, &hash).await.ok();
            base_store::set_with_file(
                folder_id,
                &relative_path,
                &hash,
                &local_root.join(&safe_rel),
            );
            logger::info("download", format!("已下载并发布: {}", relative_path));
            crate::transfers::download_progress(&final_str, "done", Some(task_id), None);
        }
        Err(PublishErr::Locked) => {
            sync_api::block_task(&client, task_id, "目标文件被占用")
                .await
                .ok();
            logger::warn(
                "download",
                format!("目标被占用，转等待解锁: {}", relative_path),
            );
            crate::transfers::download_progress(&final_str, "blocked", Some(task_id), None);
        }
        Err(PublishErr::Other(msg)) => {
            sync_api::fail_task(&client, task_id, &msg).await.ok();
            logger::error("download", format!("下载失败 {}: {}", relative_path, msg));
            crate::transfers::download_progress(&final_str, "error", Some(task_id), Some(msg));
        }
    }
}
