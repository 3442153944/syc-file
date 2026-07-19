/**
 * http.ts — web 模式的 fetch 封装，对标 Rust ApiClient
 *
 * 只在 isTauri() === false 时被调用；Tauri 模式走 invoke()。
 * 路由路径与 Rust routes.rs 保持一致（/user/login 等），无需 /v1 前缀（这里补）。
 */

import { getServerUrl, getToken } from './platform'

interface ApiEnvelope<T> {
  code: number
  message: string
  data?: T
}

/**
 * 携带业务码 + HTTP 状态码的错误。继承 Error 故 `.message`/`String(e)` 仍可用（不破坏既有 catch），
 * 另外暴露 `.code`（后端信封 code，如 401/403）与 `.status`（HTTP 状态码）供调用方按码分支处理。
 */
export class ApiError extends Error {
  code: number
  status: number
  constructor(message: string, code: number, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

async function send<T>(method: string, path: string, body?: unknown): Promise<T> {
  const url = `${getServerUrl()}/v1${path}`
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers['Token'] = token

  let init: RequestInit = { method, headers }

  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }

  let res: Response
  try {
    res = await fetch(url, init)
  } catch (e: any) {
    // 网络层失败（DNS/连接/CORS 等），无 HTTP 状态
    throw new ApiError(`网络请求失败: ${e?.message || e}`, -1, 0)
  }

  const text = await res.text()
  let json: ApiEnvelope<T>
  try {
    json = JSON.parse(text) as ApiEnvelope<T>
  } catch {
    throw new ApiError(`响应解析失败 (HTTP ${res.status}): ${text.slice(0, 200)}`, -1, res.status)
  }

  if (json.code !== 200) {
    // 后端约定即使业务失败也回 HTTP 200 + body code；把 body code 与 HTTP status 一并带出
    throw new ApiError(json.message || `请求失败 (${path})`, json.code, res.status)
  }
  // data 可为 null（更新/删除类接口成功即无数据），不视为错误
  return json.data as T
}

export function httpPost<T>(path: string, body?: unknown): Promise<T> {
  return send<T>('POST', path, body)
}

export function httpPut<T = void>(path: string, body?: unknown): Promise<T> {
  return send<T>('PUT', path, body)
}

export function httpGet<T>(path: string, params?: Record<string, string>): Promise<T> {
  const qs = params ? '?' + new URLSearchParams(params).toString() : ''
  return send<T>('GET', path + qs)
}

export function httpDelete<T = void>(path: string): Promise<T> {
  return send<T>('DELETE', path)
}

/** 构建带 token 的 GET URL（下载链接） */
export function buildGetUrl(path: string, params: Record<string, string>): string {
  const token = getToken()
  const allParams = { ...params, token }
  return `${getServerUrl()}/v1${path}?${new URLSearchParams(allParams).toString()}`
}

/**
 * 裸字节 POST：query 带 params，body 是分片二进制（application/octet-stream）。
 * 返回完整响应信封（不 throw），供分片上传按业务码区分：
 * - code==200 成功 / 422 分片校验失败需重传 / 404 会话过期需重新 init
 */
export async function httpPostRawBytes<T>(
  path: string,
  params: Record<string, string>,
  bytes: Uint8Array,
): Promise<ApiEnvelope<T>> {
  const url = `${getServerUrl()}/v1${path}?${new URLSearchParams(params).toString()}`
  const token = getToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/octet-stream',
  }
  if (token) headers['Token'] = token

  const res = await fetch(url, { method: 'POST', headers, body: bytes })
  const text = await res.text()
  try {
    return JSON.parse(text) as ApiEnvelope<T>
  } catch {
    return { code: -1, message: `响应解析失败: ${text.slice(0, 200)}`, data: undefined }
  }
}
