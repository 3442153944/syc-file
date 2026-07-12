import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpPost, httpGet, httpPut, httpDelete } from '../http'
import { getDeviceId } from '../platform'
import type { SyncFolder, SyncTask, SyncTaskPage, SyncConflict, ConflictResolution } from './syncTypes'

export async function createSyncFolder(
  name: string,
  localPath: string,
  remotePath: string,
  direction: string,
): Promise<SyncFolder> {
  if (isTauri()) return invoke<SyncFolder>('create_sync_folder', { name, localPath, remotePath, direction })
  // web 模式：device_id 从 localStorage 取
  return httpPost<SyncFolder>('/sync/folders', {
    name, local_path: localPath, remote_path: remotePath,
    direction, owner_device_id: getDeviceId(),
  })
}

export async function listSyncFolders(): Promise<SyncFolder[]> {
  if (isTauri()) return invoke<SyncFolder[]>('list_sync_folders')
  return httpGet<SyncFolder[]>('/sync/folders')
}

export async function deleteSyncFolder(folderId: number): Promise<void> {
  if (isTauri()) return invoke('delete_sync_folder', { folderId })
  await httpDelete(`/sync/folders/${folderId}`)
}

/** 更新同步文件夹（仅传需要改的字段）。 */
export async function updateSyncFolder(
  folderId: number,
  updates: { enabled?: boolean; direction?: string; name?: string },
): Promise<void> {
  if (isTauri()) return invoke('update_sync_folder', { folderId, ...updates })
  await httpPut(`/sync/folders/${folderId}`, updates)
}

export async function listPendingTasks(): Promise<SyncTask[]> {
  if (isTauri()) return invoke<SyncTask[]>('list_pending_tasks')
  return httpGet<SyncTask[]>('/sync/tasks/pending', { device_id: getDeviceId() })
}

/** 分页查询同步任务记录（历史）。status 空 = 全部状态。 */
export async function listSyncTasks(
  page: number,
  pageSize: number,
  status = '',
): Promise<SyncTaskPage> {
  if (isTauri()) return invoke<SyncTaskPage>('list_sync_tasks', { status, page, pageSize })
  return httpGet<SyncTaskPage>('/sync/tasks', {
    page: String(page), page_size: String(pageSize), ...(status ? { status } : {}),
  })
}

/** 批量清理终态任务记录（completed/failed），返回删除条数。 */
export async function clearSyncTasks(status = ''): Promise<number> {
  if (isTauri()) return invoke<number>('clear_sync_tasks', { status })
  const r = await httpDelete<{ deleted: number }>(`/sync/tasks${status ? `?status=${status}` : ''}`)
  return (r as any)?.deleted ?? 0
}

export async function listConflicts(): Promise<SyncConflict[]> {
  if (isTauri()) return invoke<SyncConflict[]>('list_conflicts')
  return httpGet<SyncConflict[]>('/sync/conflicts')
}

export async function resolveConflict(conflictId: number, resolution: ConflictResolution): Promise<void> {
  if (isTauri()) return invoke('resolve_conflict', { conflictId, resolution })
  await httpPost(`/sync/conflicts/${conflictId}/resolve`, { resolution })
}

export async function deleteConflict(conflictId: number): Promise<void> {
  if (isTauri()) return invoke('delete_conflict', { conflictId })
  await httpDelete(`/sync/conflicts/${conflictId}`)
}
