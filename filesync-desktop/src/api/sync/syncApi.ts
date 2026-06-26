import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpPost, httpGet, httpDelete } from '../http'
import { getDeviceId } from '../platform'
import type { SyncFolder, SyncTask } from './syncTypes'

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

export async function listPendingTasks(): Promise<SyncTask[]> {
  if (isTauri()) return invoke<SyncTask[]>('list_pending_tasks')
  return httpGet<SyncTask[]>('/sync/tasks/pending', { device_id: getDeviceId() })
}

export async function listConflicts(): Promise<SyncTask[]> {
  if (isTauri()) return invoke<SyncTask[]>('list_conflicts')
  return httpGet<SyncTask[]>('/sync/conflicts')
}

export async function deleteConflict(conflictId: number): Promise<void> {
  if (isTauri()) return invoke('delete_conflict', { conflictId })
  await httpDelete(`/sync/conflicts/${conflictId}`)
}
