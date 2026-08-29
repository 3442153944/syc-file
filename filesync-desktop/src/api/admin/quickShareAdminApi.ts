import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpGet, httpDelete} from '../http'

export interface AdminQuickShareItem {
    id: number
    share_code: string
    user_id: number
    username: string
    file_name: string
    file_size: number
    storage_kind: 'memory' | 'disk'
    expire_time: string
    created_at: string
}

export interface AdminQuickShareListData {
    list: AdminQuickShareItem[]
    total: number
    page: number
    page_size: number
}

/** 缓存管理：跨用户查看当前有效的粘贴快传缓存，仅管理员可调（后端会再校验一次角色）。 */
export async function listAdminQuickShare(page: number, pageSize: number, username?: string): Promise<AdminQuickShareListData> {
    const params: Record<string, string> = {page: String(page), page_size: String(pageSize)}
    if (username) params.username = username
    if (isTauri()) return invoke<AdminQuickShareListData>('api_request', {
        method: 'GET',
        path: '/admin/quick-share',
        body: null,
        query: params,
    })
    return httpGet<AdminQuickShareListData>('/admin/quick-share', params)
}

/** 缓存管理：管理员强制清理任意用户的一条粘贴快传缓存，不用等自动过期。 */
export async function revokeAdminQuickShare(shareCode: string): Promise<void> {
    const path = `/admin/quick-share/${encodeURIComponent(shareCode)}`
    if (isTauri()) {
        await invoke('api_request', {method: 'DELETE', path, body: null, query: null})
        return
    }
    await httpDelete(path)
}
