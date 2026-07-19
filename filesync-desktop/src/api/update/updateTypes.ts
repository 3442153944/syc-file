// api/update/updateTypes.ts
// 应用更新 DTO，对齐后端 internal/model/AppRelease.go + update/handler.go。

export interface AppRelease {
  id: number
  platform: string
  version_code: number
  version_name: string
  notes: string
  file_path: string
  file_name: string
  file_size: number
  file_hash: string
  mandatory: boolean
  min_version_code: number
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface PublishParams {
  platform?: string
  version_code: number
  version_name: string
  notes?: string
  file_path: string
  file_name: string
  file_size?: number
  file_hash?: string
  mandatory?: boolean
  min_version_code?: number
  enabled?: boolean
}

export interface ReleaseListData {
  list: AppRelease[]
  total: number
}

export interface CheckResult {
  has_update: boolean
  mandatory?: boolean
  release?: AppRelease
}
