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
  /** pending | syncing | completed | failed | conflict | waiting_unlock */
  sync_status: string
  relative_path: string
  file_name: string
  file_size?: number
  file_hash?: string
  progress?: number
  error?: string
  created_at: string
}

/** 冲突待办记录（GET /sync/conflicts），对应后端 sync_conflict 表 */
export interface SyncConflict {
  id: number
  user_id: number
  device_id: string
  folder_id: number
  file_id: number
  relative_path: string
  file_name: string
  server_hash?: string
  local_hash?: string
  base_hash?: string
  server_version: number
  /** pending | resolved */
  status: string
  /** accept_server | keep_local */
  resolution?: string
  created_at: string
}

/** 冲突解决方式 */
export type ConflictResolution = 'accept_server' | 'keep_local'
