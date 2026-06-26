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

  const res = await fetch(url, init)
  const json = await res.json() as ApiEnvelope<T>

  if (json.code !== 200) throw new Error(json.message || `请求失败 (${path})`)
  if (json.data === undefined || json.data === null) throw new Error(`${path}: data 为空`)
  return json.data
}

export function httpPost<T>(path: string, body?: unknown): Promise<T> {
  return send<T>('POST', path, body)
}

export function httpGet<T>(path: string, params?: Record<string, string>): Promise<T> {
  const qs = params ? '?' + new URLSearchParams(params).toString() : ''
  return send<T>('GET', path + qs)
}

export function httpDelete<T = void>(path: string): Promise<T> {
  return send<T>('DELETE', path)
}

/** 文件上传专用：body 已是 FormData，不设 Content-Type（浏览器自动加 boundary） */
export async function httpPostForm<T>(path: string, form: FormData): Promise<T> {
  const url = `${getServerUrl()}/v1${path}`
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers['Token'] = token

  const res = await fetch(url, { method: 'POST', headers, body: form })
  const json = await res.json() as ApiEnvelope<T>

  if (json.code !== 200) throw new Error(json.message || `上传失败 (${path})`)
  if (json.data === undefined || json.data === null) throw new Error(`${path}: data 为空`)
  return json.data
}

/** 构建带 token 的 GET URL（下载链接） */
export function buildGetUrl(path: string, params: Record<string, string>): string {
  const token = getToken()
  const allParams = { ...params, token }
  return `${getServerUrl()}/v1${path}?${new URLSearchParams(allParams).toString()}`
}
