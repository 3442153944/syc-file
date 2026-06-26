// api/file/response.rs
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DiskInfo {
    pub path: String,
    pub mountpoint: String,
    pub total: i64,
    pub free: i64,
    pub used: i64,
    #[serde(rename = "used_percent")]
    pub used_percent: f64,
    #[serde(rename = "total_gb")]
    pub total_gb: String,
    #[serde(rename = "free_gb")]
    pub free_gb: String,
    #[serde(rename = "is_allowed")]
    pub is_allowed: bool,
    #[serde(rename = "is_accessible")]
    pub is_accessible: bool,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AvailableDisksData {
    pub total: i32,
    #[serde(rename = "allowed_count")]
    pub allowed_count: i32,
    #[serde(rename = "allowed_disks")]
    pub allowed_disks: Vec<DiskInfo>,
    #[serde(rename = "all_disks")]
    pub all_disks: Vec<DiskInfo>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FileItem {
    pub name: String,
    pub path: String,
    #[serde(rename = "is_dir")]
    pub is_dir: bool,
    pub size: i64,
    #[serde(rename = "mod_time")]
    pub mod_time: String,
    pub extension: String,
    #[serde(rename = "children_count")]
    pub children_count: i32,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TraverseDirectoryData {
    #[serde(rename = "current_path")]
    pub current_path: String,
    #[serde(rename = "parent_path")]
    pub parent_path: String,
    pub items: Vec<FileItem>,
    #[serde(rename = "total_count")]
    pub total_count: i32,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct CheckFileData {
    pub exists: bool,
    #[serde(rename = "can_upload")]
    pub can_upload: bool,
    #[serde(rename = "file_name")]
    pub file_name: String,
    #[serde(rename = "file_size")]
    pub file_size: Option<i64>,
    pub path: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct UploadData {
    #[serde(rename = "history_id")]
    pub history_id: i64,
    #[serde(rename = "file_name")]
    pub file_name: String,
    #[serde(rename = "file_size")]
    pub file_size: i64,
    #[serde(rename = "storage_path")]
    pub storage_path: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DownloadHistoryItem {
    pub id: i64,
    #[serde(rename = "file_name")]
    pub file_name: Option<String>,
    #[serde(rename = "file_size")]
    pub file_size: Option<i64>,
    #[serde(rename = "download_status")]
    pub download_status: String,
    #[serde(rename = "created_at")]
    pub created_at: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DownloadHistoryData {
    pub list: Vec<DownloadHistoryItem>,
    pub total: i64,
}
