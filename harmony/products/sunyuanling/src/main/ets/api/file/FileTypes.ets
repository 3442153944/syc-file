/**
 * FileTypes
 * 文件域请求/响应类型。字段名直接用 snake_case 对齐后端 json tag（ArkTS 接口字段需与 JSON 一致）。
 */

// ==================== 删除文件 ====================
export interface DeleteFileParams {
  path: string;
  name: string;
}

// ==================== 获取可用磁盘 ====================
export interface AvailableDisksParams {
  disk_path: string;
  detailed: boolean;
}

export interface DiskInfo {
  path: string;
  mountpoint: string;
  device: string;
  fstype: string;
  total: number;
  free: number;
  used: number;
  used_percent: number;
  total_gb: string;
  free_gb: string;
  is_allowed: boolean;
  is_accessible: boolean;
  is_ssd: boolean;
}

export interface AvailableDisksData {
  total: number;
  allowed_count: number;
  allowed_disks: DiskInfo[];
  all_disks: DiskInfo[];
}

// ==================== 遍历目录 ====================
export interface TraverseDirectoryParams {
  path: string;
  page: number;
  page_size: number;
}

export interface FileItem {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mod_time: string;
  mode: string;
  extension: string;
  children_count: number;
}

export interface Pagination {
  page: number;
  page_size: number;
  total_pages: number;
  has_more: boolean;
}

export interface TraverseDirectoryData {
  current_path: string;
  parent_path: string;
  items: FileItem[];
  total_count: number;
  dir_count: number;
  file_count: number;
  pagination: Pagination | null;
}

// ==================== 下载 ====================
export interface DownloadParams {
  path: string;
  name: string;
  device_id: string;
}

// ==================== 分片上传 ====================
export interface UploadInitParams {
  path: string;
  name: string;
  total_size: number;
  chunk_size: number;
  chunk_count: number;
  merkle_root: string;
  file_hash: string;
  leaf_hashes: string[];
  device_id: string;
}

export interface UploadInitData {
  upload_id: string;
  instant: boolean;
  chunk_size: number;
  chunk_count: number;
  missing: number[];
}

export interface UploadStatusData {
  found: boolean;
  status: string;
  chunk_size: number;
  chunk_count: number;
  received: number;
  missing: number[];
  total_size: number;
}

export interface UploadCompleteParams {
  upload_id: string;
  device_id: string;
}

export interface UploadCompleteData {
  file_id: number;
  file_name: string;
  storage_path: string;
  file_size: number;
  file_hash: string;
  synced: boolean;
}

export interface UploadChunkData {
  index: number;
  received: number;
  chunk_count: number;
  complete: boolean;
}

// ==================== 下载历史 ====================
export interface DownloadHistoryParams {
  pageNum: number;
  pageSize: number;
}

export interface DownloadHistoryItem {
  id: number;
  user_id: number;
  device_id: number;
  file_id: number | null;
  file_name: string | null;
  file_size: number | null;
  download_status: string;
  download_speed: number | null;
  ip_address: string | null;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
}

export interface DownloadHistoryData {
  list: DownloadHistoryItem[];
  total: number;
  pageNum: number;
  pageSize: number;
}

export interface DeleteDownloadHistoryParams {
  ids: number[];
}
