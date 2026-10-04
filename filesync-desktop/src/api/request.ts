// api/request.ts
// 统一请求入口：Tauri 模式走 Rust 侧的通用代理命令 `api_request`（token / 服务器地址由 Rust 的 SyncConfig 提供，
// 不依赖 webview 里可能过期的 localStorage token）；Web 模式走 http.ts 的 fetch。
// 新增的、形态简单的接口都用它，不必再逐个包 Rust command。
import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpGet, httpPost, httpPut, httpDelete } from './http'

export async function request<T>(
  method: 'GET' | 'POST' | 'PUT' | 'DELETE',
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
    case 'PUT':
      return httpPut<T>(path, opts.body)
    case 'DELETE': {
      const qs = opts.query ? `?${new URLSearchParams(opts.query).toString()}` : ''
      return httpDelete<T>(`${path}${qs}`)
    }
  }
}

/** 去掉空值，避免把 `?keyword=` 这种空筛选条件发上去。 */
export function clean(params: Record<string, string | number | undefined | null>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') out[k] = String(v)
  }
  return out
}
