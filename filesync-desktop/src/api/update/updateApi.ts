// api/update/updateApi.ts
// 应用更新 API。与 sync/file 一致：Tauri 走 invoke → Rust ApiClient（token 来自 SyncConfig，
// reqwest 直发，避免 webview fetch 的 CORS / localStorage 陈旧 token 导致 401）；Web 走 http.ts fetch。
import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpGet, httpPost, httpPut, httpDelete } from '../http'
import type { AppRelease, PublishParams, ReleaseListData, CheckResult } from './updateTypes'

/** 发布新版本（仅 admin）。APK 须已上传落在允许盘内，file_path 用上传返回的 storage_path。 */
export async function publishRelease(params: PublishParams): Promise<AppRelease> {
  if (isTauri()) return invoke<AppRelease>('publish_app_release', { release: params })
  return httpPost<AppRelease>('/update/publish', params)
}

/** 版本列表（仅 admin）。 */
export async function listReleases(platform = 'android'): Promise<ReleaseListData> {
  if (isTauri()) return invoke<ReleaseListData>('list_app_releases', { platform })
  return httpGet<ReleaseListData>('/update/releases', { platform })
}

/** 修改版本：上/下架、强制、说明、min_version_code（仅 admin）。 */
export async function updateRelease(
  id: number,
  updates: { enabled?: boolean; mandatory?: boolean; notes?: string; min_version_code?: number },
): Promise<void> {
  if (isTauri()) {
    await invoke('update_app_release', { id, updates })
    return
  }
  await httpPut(`/update/releases/${id}`, updates)
}

/** 删除版本记录（仅 admin，不删磁盘 APK）。 */
export async function deleteRelease(id: number): Promise<void> {
  if (isTauri()) {
    await invoke('delete_app_release', { id })
    return
  }
  await httpDelete(`/update/releases/${id}`)
}

/** 检查更新（任意登录用户）。 */
export async function checkUpdate(versionCode: number, platform = 'android'): Promise<CheckResult> {
  if (isTauri()) return invoke<CheckResult>('check_app_update', { platform, versionCode })
  return httpGet<CheckResult>('/update/check', { platform, version_code: String(versionCode) })
}
