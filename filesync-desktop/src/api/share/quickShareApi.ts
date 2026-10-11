import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, httpPostBlob} from '../http'
import type {CreateShareLinkData, QuickShareQuota} from '../file/fileTypes'

/**
 * 粘贴快传上传。
 *
 * Tauri：走 Rust 侧多路分片上传核心（探测 → 选片 → DRR 多路调度 → AIMD，h1.1 多连接），
 * 绕开 WebView2 原生 XHR 单连接 h2 被 64KB 流控窗口钉死在 ~880KB/s 的问题。进度经
 * "quick-share-progress" 事件回来，返回的分享记录与旧接口同一形状。
 *
 * Web：单次整包 POST。走 XHR（httpPostBlob）而不是 fetch：整包上传只有一个请求，
 * fetch 拿不到上传进度，大文件时界面只能干转圈。XHR 能按实际发出的字节回调进度，
 * 同时直接流式发送 File，不用先把整个文件读进内存。
 *
 * 注意进度到 100% 不代表完成：字节发完后服务端还要落盘、建分享链接，
 * 这段时间调用方应显示「处理中」而不是「已完成」。
 */
export async function quickSharePaste(
    file: File,
    onProgress?: (sent: number, total: number) => void,
): Promise<CreateShareLinkData> {
    const name = file.name || 'pasted-file'
    if (isTauri()) {
        const {listen} = await import('@tauri-apps/api/event')
        const unlisten = await listen<{ sent: number; total: number }>('quick-share-progress', e => {
            onProgress?.(e.payload.sent, e.payload.total)
        })
        try {
            // Vec<u8> 经 JSON 数组传参（number[]），Uint8Array 直接传会序列化成对象
            const bytes = Array.from(new Uint8Array(await file.arrayBuffer()))
            return await invoke<CreateShareLinkData>('quick_share_upload', {name, bytes})
        } finally {
            unlisten()
        }
    }
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
