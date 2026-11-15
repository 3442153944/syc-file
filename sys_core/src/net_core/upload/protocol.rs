use serde::{Deserialize, Serialize};

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
    /// 目标同名时的策略：""/"reject"=报错（默认，同步链路用这个），"timestamp"=服务端自动加时间戳
    #[serde(rename = "on_conflict")]
    #[serde(default)]
    #[serde(skip_serializing_if = "String::is_empty")]
    pub on_conflict: String,
    /// 同步链路的覆盖式上传：目标已存在时服务端按 base_hash 做版本检查，
    /// 一致则快进覆盖，不一致（或无基线）则返回 409 并转入冲突待办。
    #[serde(default)]
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub sync: bool,
    #[serde(default)]
    #[serde(skip_serializing_if = "String::is_empty")]
    pub base_hash: String,
    /// 粘贴快传模式：服务端自算目标路径（quick_share.base_path/<userID>/<uuid><ext>）、
    /// 按快传配额预检，complete 直接生成分享记录（响应为分享数据结构）
    #[serde(rename = "quick_share")]
    #[serde(default)]
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub quick_share: bool,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct UploadCompleteParams {
    #[serde(rename = "upload_id")]
    pub upload_id: String,
    /// 本机设备 id，服务端派发同步任务时排除源设备
    #[serde(rename = "device_id")]
    pub device_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UploadInitData {
    #[serde(rename = "upload_id")]
    pub upload_id: String,
    /// true=秒传，已直接完成，无需上传分片
    #[serde(default)]
    pub instant: bool,
    #[serde(rename = "chunk_size")]
    #[serde(default)]
    pub chunk_size: i64,
    #[serde(rename = "chunk_count")]
    #[serde(default)]
    pub chunk_count: i32,
    /// 仍缺失（需上传）的分片索引
    #[serde(default)]
    pub missing: Vec<i32>,
    /// 服务端最终采用的文件名。**可能与本地文件名不同**——同名冲突策略为 timestamp 时
    /// 服务端会自动加时间戳。秒传/已存在分支才返回，普通分支为空。
    #[serde(rename = "file_name")]
    #[serde(default)]
    pub file_name: String,
    /// 服务端最终落盘的完整路径（同上，仅秒传/已存在分支返回）。
    #[serde(rename = "storage_path")]
    #[serde(default)]
    pub storage_path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UploadChunkData {
    #[serde(default)]
    pub index: i32,
    #[serde(default)]
    pub received: i32,
    #[serde(rename = "chunk_count")]
    #[serde(default)]
    pub chunk_count: i32,
    /// 是否已收齐全部分片
    #[serde(default)]
    pub complete: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct QuickShareCompleteData {
    /// 客户端确认可下载的入口，不使用上传启动时可能已失效的地址。
    #[serde(default)]
    pub entry_url: String,
    #[serde(default)]
    pub entry_candidates: Vec<String>,
    #[serde(rename = "share_code")]
    pub share_code: String,
    #[serde(rename = "temp_name")]
    pub temp_name: String,
    #[serde(rename = "file_name")]
    pub file_name: String,
    #[serde(rename = "file_size")]
    pub file_size: i64,
    #[serde(rename = "is_dir")]
    #[serde(default)]
    pub is_dir: bool,
    #[serde(rename = "expire_time")]
    pub expire_time: String,
    #[serde(rename = "url_path")]
    pub url_path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UploadCompleteData {
    #[serde(rename = "file_id")]
    #[serde(default)]
    pub file_id: u64,
    #[serde(rename = "file_name")]
    pub file_name: String,
    #[serde(rename = "storage_path")]
    pub storage_path: String,
    #[serde(rename = "file_size")]
    pub file_size: i64,
    #[serde(rename = "file_hash")]
    #[serde(default)]
    pub file_hash: String,
    /// 是否已触发同步派发（目标在同步目录内）
    #[serde(default)]
    pub synced: bool,
}
