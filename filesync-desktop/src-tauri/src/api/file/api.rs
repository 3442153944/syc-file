// api/file/api.rs
// 职责：文件相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 ApiClient，不包含业务逻辑。
use super::{params::*, response::*};
use crate::api::{client::{ApiClient, ApiResponse}, routes};
use reqwest::multipart;
use std::collections::HashMap;

pub async fn get_available_disks(
    client: &ApiClient,
    params: AvailableDisksParams,
) -> Result<ApiResponse<AvailableDisksData>, String> {
    client.post(routes::FILE_AVAILABLE_DISKS, &params).await
}

pub async fn traverse_directory(
    client: &ApiClient,
    params: TraverseDirectoryParams,
) -> Result<ApiResponse<TraverseDirectoryData>, String> {
    client.post(routes::FILE_TRAVERSE_DIRECTORY, &params).await
}

/// 构建完整下载 URL（token 放 query string，供 reqwest/Range 直接使用）
pub fn build_download_url(client: &ApiClient, params: &DownloadParams) -> String {
    let mut map = HashMap::new();
    map.insert("path", params.path.clone());
    map.insert("name", params.name.clone());
    if !params.device_id.is_empty() {
        map.insert("device_id", params.device_id.clone());
    }
    client.build_url_with_token(routes::FILE_DOWNLOAD, map)
}

/// 检查文件是否已存在（action=check，JSON body）
pub async fn check_file(
    client: &ApiClient,
    params: CheckFileParams,
) -> Result<ApiResponse<CheckFileData>, String> {
    client.post(routes::FILE_UPLOAD, &params).await
}

/// 上传文件（action=upload，multipart；调用方传入已构造好的 Form）
pub async fn upload_file(
    client: &ApiClient,
    form: multipart::Form,
) -> Result<ApiResponse<UploadData>, String> {
    client.post_multipart(routes::FILE_UPLOAD, form).await
}

/// 删除远端文件（文件管理用）
pub async fn delete_file(
    client: &ApiClient,
    params: DeleteFileParams,
) -> Result<ApiResponse<serde_json::Value>, String> {
    client.post(routes::FILE_DELETE, &params).await
}

pub async fn get_download_history(
    client: &ApiClient,
    params: DownloadHistoryParams,
) -> Result<ApiResponse<DownloadHistoryData>, String> {
    client.post(routes::FILE_DOWNLOAD_HISTORY, &params).await
}

pub async fn delete_download_history(
    client: &ApiClient,
    params: DeleteDownloadHistoryParams,
) -> Result<ApiResponse<serde_json::Value>, String> {
    client.post(routes::FILE_DELETE_DOWNLOAD_HISTORY, &params).await
}
