import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, httpPostBlob} from '../http'
import type {CreateShareLinkData, QuickShareQuota} from '../file/fileTypes'

/**
 * 粘贴快传上传：单次整包 POST，不走分片协议（量小求快）。
 *
 * 走 XHR（httpPostBlob）而不是 fetch：整包上传只有一个请求，fetch 拿不到上传进度，
 * 大文件时界面只能干转圈。XHR 能按实际发出的字节回调进度，同时直接流式发送 File，
 * 不用先把整个文件读进内存。
 *
 * Web 和 Tauri 通用：Tauri 下 webview 直接请求当前节点（后端 CORS 是 *，
 * token 登录时已同步进 localStorage），不需要按 isTauri() 分支、也不需要新的 Tauri 命令。
 *
 * 注意进度到 100% 不代表完成：字节发完后服务端还要落盘、建分享链接，
 * 这段时间调用方应显示「处理中」而不是「已完成」。
 */
export async function quickSharePaste(
    file: File,
    onProgress?: (sent: number, total: number) => void,
): Promise<CreateShareLinkData> {
    const name = file.name || 'pasted-file'
    const resp = await httpPostBlob<CreateShareLinkData>('/file/quick-share/upload', {name}, file, onProgress)
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
