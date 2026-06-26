export interface SyncFolder {
  id: number
  user_id: number
  name: string
  local_path: string
  remote_path: string
  /** two_way | upload_only | download_only */
  direction: string
  enabled: boolean
  owner_device_id: string
  created_at: string
  updated_at: string
}

export interface SyncTask {
  id: number
  folder_id: number
  /** download | delete | mkdir */
  task_type: string
  /** pending | syncing | completed | failed | conflict */
  sync_status: string
  relative_path: string
  file_name: string
  file_size?: number
  file_hash?: string
  progress?: number
  error?: string
  created_at: string
}
