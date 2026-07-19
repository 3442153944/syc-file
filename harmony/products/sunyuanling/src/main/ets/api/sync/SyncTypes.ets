/**
 * SyncTypes
 * 同步域 DTO，字段对齐后端 GORM 模型 json tag（snake_case）。
 * 来源：new_server/internal/model/SyncTask.go / SyncConflict.go / SyncFolder.go
 * 与 internal/sync/types.go、handler.go 的绑定结构。
 */

/** 同步任务（GET /sync/tasks、GET /sync/tasks/pending） */
export interface SyncTaskInfo {
  id: number;
  user_id: number;
  source_device_id: string;
  target_device_id: string;
  folder_id: number;
  file_id: number;
  task_type: string;
  sync_status: string;
  direction: string;
  relative_path: string;
  file_name: string;
  file_size: number;
  file_hash: string | null;
  source_hash: string | null;
  base_hash: string | null;
  conflict: boolean;
  progress: number;
  priority: number;
  retry_count: number;
  max_retry: number;
  error_message: string | null;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

/** 同步冲突记录 */
export interface SyncConflictInfo {
  id: number;
  user_id: number;
  device_id: string;
  folder_id: number;
  file_id: number;
  relative_path: string;
  file_name: string;
  server_hash: string | null;
  local_hash: string | null;
  base_hash: string | null;
  server_version: number;
  status: string;
  resolution: string | null;
  resolved_at: string | null;
  created_at: string;
  updated_at: string;
}

/** 同步文件夹 */
export interface SyncFolderInfo {
  id: number;
  user_id: number;
  name: string;
  local_path: string;
  remote_path: string;
  direction: string;
  enabled: boolean;
  excludes: string;
  owner_device_id: string;
  created_at: string;
  updated_at: string;
}

/** 解决冲突请求体 */
export interface ResolveConflictParams {
  resolution: string; // accept_server / keep_local
}

/** 单文件变更上报 */
export interface SyncNotifyParams {
  device_id: string;
  folder_id: number;
  relative_path: string;
  file_name: string;
  action: string; // create / modify / delete
  file_size: number;
  file_hash: string;
  base_hash: string;
  is_dir: boolean;
  mtime: number;
}

/** 扫描清单单项 */
export interface ScanItemDto {
  relative_path: string;
  file_name: string;
  file_size: number;
  file_hash: string;
  is_dir: boolean;
  mtime: number;
}

/** 全量扫描上报 */
export interface SyncScanParams {
  device_id: string;
  folder_id: number;
  items: ScanItemDto[];
}

export interface TaskCompleteParams {
  file_hash: string;
}

export interface TaskFailedParams {
  error: string;
}

export interface TaskBlockedParams {
  reason: string;
}

/** WS 同步事件 S→C：task_created */
export interface WsTaskCreatedEvent {
  event: string; // "task_created"
  task_id: number;
  task_type: string; // download / delete / mkdir
  direction: string;
  folder_id: number;
  relative_path: string;
  file_name: string;
  file_size: number;
  file_hash: string | null;
  remote_path: string;
  remote_dir: string;
}

/** WS 同步事件 S→C：conflict */
export interface WsConflictEvent {
  event: string; // "conflict"
  conflict_id: number;
  folder_id: number;
  relative_path: string;
  file_name: string;
  server_hash: string;
  server_version: number;
  base_hash: string;
  local_hash: string;
}

/** WS 同步事件 S→C：conflict_resolved */
export interface WsConflictResolvedEvent {
  event: string; // "conflict_resolved"
  conflict_id: number;
  resolution: string; // accept_server / keep_local
  server_hash: string;
}

/** WS 消息信封 */
export interface WsMessageEnvelope {
  id?: string;
  type?: string; // file_sync / text / ...
  content?: Object | null;
  timestamp?: number;
}
