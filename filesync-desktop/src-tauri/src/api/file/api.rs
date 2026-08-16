// api/file/api.rs
// 职责：文件相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 ApiClient，不包含业务逻辑。
use super::{params::*, response::*};
use crate::api::{
    client::{ApiClient, ApiResponse},
    routes,
};
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

/// 删除远端文件（文件管理用 + 同步场景 delete-before-upload）
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
    client
        .post(routes::FILE_DELETE_DOWNLOAD_HISTORY, &params)
        .await
}

// ==================== 分片上传 ====================

/// 初始化分片上传：提交描述信息，返回 upload_id + 缺失分片（instant=true 为秒传已完成）。
pub async fn upload_init(
    client: &ApiClient,
    params: UploadInitParams,
) -> Result<ApiResponse<UploadInitData>, String> {
    client.post(routes::FILE_UPLOAD_INIT, &params).await
}

/// 上传单个分片：query 带 upload_id+index，body 是分片裸字节。
///
/// 返回原始 `ApiResponse` 由上层按业务码区分：
/// - `code == 200` → 该片成功
/// - `code == 422` → ChunkVerifyException（分片校验失败，需重传该片）
/// - `code == 404` → SessionGoneException（会话过期，需重新 init）
pub async fn upload_chunk(
    client: &ApiClient,
    upload_id: &str,
    index: i32,
    data: Vec<u8>,
) -> Result<ApiResponse<UploadChunkData>, String> {
    let index_str = index.to_string();
    let params: [(&str, &str); 2] = [("upload_id", upload_id), ("index", index_str.as_str())];
    client
        .post_raw_bytes(routes::FILE_UPLOAD_CHUNK, &params, data)
        .await
}

/// 完成分片上传：收齐后触发服务端校验落盘。
pub async fn upload_complete(
    client: &ApiClient,
    params: UploadCompleteParams,
) -> Result<ApiResponse<UploadCompleteData>, String> {
    client.post(routes::FILE_UPLOAD_COMPLETE, &params).await
}
