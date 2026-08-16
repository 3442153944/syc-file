// api/clipboard/clipboardApi.ts
// 剪贴板同步 API。Tauri 走通用代理命令 api_request，Web 走 http.ts。
//
// 注意：桌面端「推送本机剪贴板」不走这里——前端读不到系统剪贴板，
// 由 Rust 侧的 push_clipboard_now 命令读取后直接调服务端（见 clipboard_sync.rs）。
// 本模块提供的是历史查询/清空，以及 Web 模式下的推送。
import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpGet, httpPost, httpDelete } from '../http'
import { getDeviceId } from '../platform'

export interface ClipItem {
  id: string
  user_id: number
  device_id: string
  device_name: string
  content_type: string
  content: string
  size: number
  created_at: number
}

async function request<T>(
  method: 'GET' | 'POST' | 'DELETE',
  path: string,
  opts: { query?: Record<string, string>; body?: unknown } = {},
): Promise<T> {
  if (isTauri()) {
    return invoke<T>('api_request', {
      method,
      path,
      body: opts.body ?? null,
      query: opts.query ?? null,
    })
  }
  switch (method) {
    case 'GET':
      return httpGet<T>(path, opts.query)
    case 'POST':
      return httpPost<T>(path, opts.body)
    case 'DELETE':
      return httpDelete<T>(path)
  }
}

/** 推送一段文本到其它设备（Web 模式用；桌面端用 Rust 命令 push_clipboard_now）。 */
export function pushClipboard(content: string, deviceName = ''): Promise<{ item: ClipItem; delivered: number }> {
  return request('POST', '/clipboard/push', {
    body: {
      content,
      content_type: 'text',
      device_id: getDeviceId(),
      device_name: deviceName,
    },
  })
}

export function getClipboardHistory(limit = 20): Promise<ClipItem[]> {
  return request('GET', '/clipboard/history', { query: { limit: String(limit) } })
}

export function clearClipboardHistory(): Promise<null> {
  return request('DELETE', '/clipboard/history')
}
