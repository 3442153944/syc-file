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

export interface UploadData {
    history_id: number
    file_name: string
    file_size: number
    storage_path: string
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
