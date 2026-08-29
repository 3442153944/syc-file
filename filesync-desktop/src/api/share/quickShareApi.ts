import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, httpPostRawBytes} from '../http'
import type {CreateShareLinkData, QuickShareQuota} from '../file/fileTypes'

/**
 * 粘贴快传上传：单次原始字节 POST，不走分片协议（量小求快）。
 * httpPostRawBytes 本身就同时兼容 Web 和 Tauri（Tauri 下 getServerUrl() 直接指向
 * localhost:8991，后端 CORS 是 *，token 登录时已同步进 localStorage），所以这里
 * 不需要按 isTauri() 分支、也不需要新的 Tauri 命令。
 */
export async function quickSharePaste(file: File): Promise<CreateShareLinkData> {
    const bytes = new Uint8Array(await file.arrayBuffer())
    const name = file.name || 'pasted-file'
    const resp = await httpPostRawBytes<CreateShareLinkData>('/file/quick-share/upload', {name}, bytes)
    if (resp.code !== 200) {
        throw new Error(resp.message || '快速分享上传失败')
    }
    return resp.data as CreateShareLinkData
}

export async function getQuickShareQuota(): Promise<QuickShareQuota> {
    if (isTauri()) return invoke<QuickShareQuota>('api_request', {
        method: 'POST',
        path: '/file/quick-share/quota',
        body: {},
        query: null,
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
