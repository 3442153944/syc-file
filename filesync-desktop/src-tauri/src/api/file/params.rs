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
#[derive(Debug, Serialize, Deserialize)]
pub struct UploadInitParams {
    pub path: String,
    pub name: String,
    #[serde(rename = "total_size")]
    pub total_size: i64,
    #[serde(rename = "chunk_size")]
    pub chunk_size: i64,
    #[serde(rename = "chunk_count")]
    pub chunk_count: i32,
    /// blake3 Merkle 树根 hex
    #[serde(rename = "merkle_root")]
    pub merkle_root: String,
    /// 整文件 blake3 hex（秒传/去重键）
    #[serde(rename = "file_hash")]
    pub file_hash: String,
    /// 每片叶子哈希 hex，长度须等于 chunk_count
    #[serde(rename = "leaf_hashes")]
    pub leaf_hashes: Vec<String>,
    /// 本机设备 id：秒传在 init 阶段直接完成，服务端同步派发需排除源设备
    #[serde(rename = "device_id")]
    #[serde(default)]
    pub device_id: String,
}

/// 分片上传完成。
#[derive(Debug, Serialize, Deserialize)]
pub struct UploadCompleteParams {
    #[serde(rename = "upload_id")]
    pub upload_id: String,
    /// 本机设备 id，服务端派发同步任务时排除源设备
    #[serde(rename = "device_id")]
    pub device_id: String,
}

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
