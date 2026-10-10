// commands/file.rs
// 职责：文件域——磁盘/目录浏览、上传、删除、下载 URL、下载历史。
use crate::api::file::{api as file_api, params::*, response::*};
use crate::commands::support::{api_data, ensure_ok, make_client};
use crate::config::SharedSyncConfig;
use crate::logger;
use crate::transfers;
use std::sync::Arc;
use tauri::State;

#[tauri::command]
pub async fn get_available_disks(
    config: State<'_, SharedSyncConfig>,
) -> Result<AvailableDisksData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::get_available_disks(
        &client,
        AvailableDisksParams {
            disk_path: String::new(),
            detailed: true,
        },
    )
    .await?;
    api_data(resp, "get_available_disks")
}

#[tauri::command]
pub async fn traverse_directory(
    path: String,
    page: i32,
    page_size: i32,
    config: State<'_, SharedSyncConfig>,
) -> Result<TraverseDirectoryData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::traverse_directory(
        &client,
        TraverseDirectoryParams {
            path,
            page,
            page_size,
        },
    )
    .await?;
    api_data(resp, "traverse_directory")
}

/// 上传文件：TS 传本地绝对路径，Rust 走分片上传（blake3 + 乱序并发 + 断点续传 + 秒传）。
///
/// 进度与结果记进 transfers.rs（传输列表的唯一来源），而不是只回给调用方：
/// 主窗口关闭会销毁 WebView，发起调用的页面可能已经不在了，但上传照常跑完，
/// 窗口重建后传输列表里仍能看到它。
#[tauri::command]
pub async fn upload_file(
    local_path: String,
    remote_dir: String,
    // on_conflict：目标同名时的策略。None/"reject"=报错；"timestamp"=服务端自动加时间戳区分
    // （发布 APK 用这个：每次 build 出来都叫同一个名字，报错没意义）
    on_conflict: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<UploadCompleteData, String> {
    use crate::chunked_uploader::{upload, ProgressFn, UploadOptions};
    let path = std::path::Path::new(&local_path);
    let name = path
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .unwrap_or_else(|| local_path.clone());
    let total = std::fs::metadata(path).map(|m| m.len() as i64).unwrap_or(0);
    // 先登记再做任何检查：失败也要在传输列表里留下一条带原因的记录
    let transfer_id = transfers::upload_begin(&name, "manual", total, None);

    let result: Result<UploadCompleteData, String> = async {
        if !path.exists() {
            return Err(format!("文件不存在: {}", local_path));
        }
        let (client, device_id) = {
            let cfg = config.read();
            (make_client(&cfg)?, cfg.device_id.clone())
        };
        let mut options = UploadOptions::new(device_id);
        if on_conflict.as_deref() == Some("timestamp") {
            options = options.with_timestamp_on_conflict();
        }
        let id_for_progress = transfer_id.clone();
        let on_progress: ProgressFn = Arc::new(move |sent, total| {
            transfers::upload_progress(&id_for_progress, sent, total);
        });
        let data = upload(&client, path, &remote_dir, &options, on_progress, &config).await?;
        logger::info(
            "upload",
            format!(
                "上传成功: {} ({} bytes, synced={})",
                data.storage_path, data.file_size, data.synced
            ),
        );
        Ok(data)
    }
    .await;

    transfers::upload_finish(&transfer_id, result.as_ref().err().cloned());
    result
}

/// 删除远端文件（文件管理用）
#[tauri::command]
pub async fn delete_file(
    path: String,
    name: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp =
        file_api::delete_file(&client, crate::api::file::params::DeleteFileParams { path, name }).await?;
    ensure_ok(resp)
}

/// 构建带 token 的缩略图 URL，前端可直接放进 <img>（图片/视频封面/音频封面）
#[tauri::command]
pub fn build_thumbnail_url(
    path: String,
    name: String,
    width: u32,
    version: u64,
    config: State<SharedSyncConfig>,
) -> Result<String, String> {
    let client = make_client(&config.read())?;
    Ok(file_api::build_thumbnail_url(
        &client, &path, &name, width, version,
    ))
}

/// 构建带 token 的完整下载 URL，前端可直接用于下载
#[tauri::command]
pub fn build_download_url(
    path: String,
    name: String,
    device_id: String,
    config: State<SharedSyncConfig>,
) -> Result<String, String> {
    let client = make_client(&config.read())?;
    Ok(file_api::build_download_url(
        &client,
        &DownloadParams {
            path,
            name,
            device_id,
        },
    ))
}

#[tauri::command]
pub async fn get_download_history(
    page_num: i32,
    page_size: i32,
    config: State<'_, SharedSyncConfig>,
) -> Result<DownloadHistoryData, String> {
    let client = make_client(&config.read())?;
    let resp = file_api::get_download_history(
        &client,
        DownloadHistoryParams {
            page_num,
            page_size,
        },
    )
    .await?;
    api_data(resp, "get_download_history")
}

#[tauri::command]
pub async fn delete_download_history(
    ids: Vec<i64>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = file_api::delete_download_history(&client, DeleteDownloadHistoryParams { ids }).await?;
    ensure_ok(resp)
}
