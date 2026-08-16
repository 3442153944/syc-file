export interface DiskInfo {
    path: string
    mountpoint: string
    total: number
    free: number
    used: number
    used_percent: number
    total_gb: string
    free_gb: string
    is_allowed: boolean
    is_accessible: boolean
}

export interface AvailableDisksData {
    total: number
    allowed_count: number
    allowed_disks: DiskInfo[]
    all_disks: DiskInfo[]
}

export interface FileItem {
    name: string
    path: string
    is_dir: boolean
    size: number
    mod_time: string
    extension: string
    children_count: number
}

export interface TraverseDirectoryData {
    current_path: string
    parent_path: string
    items: FileItem[]
    total_count: number
}

// ==================== 分片上传 ====================
// 对应后端 upload_chunked.go / upload_chunk.go / upload_complete.go

export interface UploadInitData {
    upload_id: string
    /** true=秒传，已直接完成，无需上传分片 */
    instant: boolean
    chunk_size: number
    chunk_count: number
    /** 仍缺失（需上传）的分片索引 */
    missing: number[]
    /** 服务端最终采用的文件名（同名冲突加过时间戳时与本地不同）。仅秒传/已存在分支返回。 */
    file_name?: string
    /** 服务端最终落盘的完整路径。仅秒传/已存在分支返回。 */
    storage_path?: string
}

export interface UploadChunkData {
    index: number
    received: number
    chunk_count: number
    /** 是否已收齐全部分片 */
    complete: boolean
}

export interface UploadCompleteData {
    file_id: number
    file_name: string
    storage_path: string
    file_size: number
    file_hash: string
    /** 是否已触发同步派发（目标在同步目录内） */
    synced: boolean
}

export interface DownloadHistoryItem {
    id: number
    file_name?: string
    file_size?: number
    download_status: string
    created_at: string
}

export interface DownloadHistoryData {
    list: DownloadHistoryItem[]
    total: number
}
