import { request } from '@/api/request'

export interface UploadSpace {
  id: number
  user_id: number
  pool_id: number
  quota_bytes: number
  used_bytes: number
	 own_used_bytes: number
	 allocated_bytes: number
	 parent_id: number | null
  file_count: number
  expires_at: string | null
  enabled: boolean
  frozen: boolean
  pool_name: string
  path: string
  default_path?: string
}

export interface StoragePool {
  id: number
  name: string
  root_path: string
  enabled: boolean
}

export interface SpaceUser {
  user_id: number
  username: string
  level: number
  space: UploadSpace | null
  error?: string
}

export interface Partition {
  path: string
  device: string
  total: number
  free: number
}

export const myUploadSpace = () => request<UploadSpace | null>('GET', '/storage/space')
export const listStoragePools = () => request<StoragePool[]>('GET', '/admin/storage-pools')
export const createStoragePool = (name: string, rootPath: string) =>
  request<StoragePool>('POST', '/admin/storage-pools', { body: { name, root_path: rootPath } })
export const setPoolEnabled = (id: number, enabled: boolean) =>
  request<null>('PUT', `/admin/storage-pools/${id}`, { body: { enabled } })
export const listStoragePartitions = () => request<Partition[]>('GET', '/admin/storage-partitions')
export const listUploadSpaces = () => request<SpaceUser[]>('GET', '/admin/upload-spaces')
export const saveUploadSpace = (userId: number, body: {
  pool_id: number
  quota_bytes: number
  expires_at: string | null
  enabled: boolean
}) => request<null>('PUT', `/admin/upload-spaces/${userId}`, { body })
