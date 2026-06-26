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

/// 上传检查参数（JSON body，action=check）
#[derive(Debug, Serialize, Deserialize)]
pub struct CheckFileParams {
    pub path: String,
    pub name: String,
    pub action: String,
}

impl CheckFileParams {
    pub fn new(path: &str, name: &str) -> Self {
        CheckFileParams {
            path: path.to_string(),
            name: name.to_string(),
            action: "check".to_string(),
        }
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DownloadHistoryParams {
    #[serde(rename = "pageNum")]
    pub page_num: i32,
    #[serde(rename = "pageSize")]
    pub page_size: i32,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DeleteDownloadHistoryParams {
    pub ids: Vec<i64>,
}
