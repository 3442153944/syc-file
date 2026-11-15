// api/file/params.rs
use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize, Default)]
pub struct AvailableDisksParams {
    pub disk_path: String,
    pub detailed: bool,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TraverseDirectoryParams {
    pub path: String,
    pub page: i32,
    pub page_size: i32,
}

/// 下载参数，token 由 ApiClient.build_url_with_token 拼入 query
#[derive(Debug)]
pub struct DownloadParams {
    pub path: String,
    pub name: String,
    pub device_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DownloadHistoryParams {
    #[serde(rename = "pageNum")]
    pub page_num: i32,
    #[serde(rename = "pageSize")]
    pub page_size: i32,
}

// ==================== 分片上传 ====================

/// 分片上传初始化描述信息。所有哈希均为 blake3 hex（与服务端 file_lib 一致）。
pub use sys_core::upload::protocol::UploadInitParams;

/// 分片上传完成。
pub use sys_core::upload::protocol::UploadCompleteParams;

#[derive(Debug, Serialize, Deserialize)]
pub struct DeleteDownloadHistoryParams {
    pub ids: Vec<i64>,
}

/// 删除远端文件参数（文件管理用）
#[derive(Debug, Serialize, Deserialize)]
pub struct DeleteFileParams {
    pub path: String,
    pub name: String,
}
