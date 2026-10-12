import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, httpPostBlob} from '../http'
import type {CreateShareLinkData, QuickShareQuota} from '../file/fileTypes'

/** 桌面返回后台任务 ID，进度和结果进入统一 transfer 快照。 */
export function startQuickSharePath(path: string): Promise<string> {
    return invoke('quick_share_upload_path', {path})
}
export function startQuickShareBytes(name: string, bytes: Uint8Array): Promise<string> {
    return invoke('quick_share_upload', {name, bytes: Array.from(bytes)})
}
export function controlQuickShare(id: string, action: 'pause' | 'resume' | 'cancel'): Promise<void> {
    return invoke('control_quick_share', {id, action})
}
/** Web 直接发送 File，不把大文件读进内存。 */
export async function quickSharePaste(file: File, onProgress?: (sent: number, total: number) => void, signal?: AbortSignal): Promise<CreateShareLinkData> {
    const resp = await httpPostBlob<CreateShareLinkData>('/file/quick-share/upload', {name: file.name || 'pasted-file'}, file, onProgress, signal)
    if (resp.code !== 200) throw new Error(resp.message || '快速分享上传失败')
    return resp.data as CreateShareLinkData
}
export async function getQuickShareQuota(): Promise<QuickShareQuota> {
    if (isTauri()) return invoke<QuickShareQuota>('api_request', {
        method: 'POST', path: '/file/quick-share/quota', body: {}, query: null,
    })
    return httpPost<QuickShareQuota>('/file/quick-share/quota')
}
export async function saveQuickShareSettings(hotkey: string, expireMinutes: number): Promise<void> {
    const body = {hotkey, expire_minutes: expireMinutes}
    if (isTauri()) {
        await invoke('api_request', {method: 'POST', path: '/user/quick-share-settings', body, query: null})
        return
    }
    await httpPost('/user/quick-share-settings', body)
}
