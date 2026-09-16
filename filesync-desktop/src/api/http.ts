/**
 * http.ts — web 模式的 fetch 封装，对标 Rust ApiClient
 *
 * 只在 isTauri() === false 时被调用；Tauri 模式走 invoke()。
 * 路由路径与 Rust routes.rs 保持一致（/user/login 等），无需 /v1 前缀（这里补）。
 */

import { getServerUrl, getToken } from './platform'
import { registerAbort } from './net'

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
  // 基地址现取：切节点后 getServerUrl() 立刻返回新节点，不用重启页面
  const url = `${getServerUrl()}/v1${path}`
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers['Token'] = token

  // 登记到在途集合：切节点时统一 abort，不必干等这条打在旧地址上的请求超时
  const ctrl = new AbortController()
  const unregister = registerAbort(ctrl)

  let init: RequestInit = { method, headers, signal: ctrl.signal }

  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }

  let res: Response
  try {
    res = await fetch(url, init)
  } catch (e: any) {
    // 网络层失败（DNS/连接/CORS/被 abort），无 HTTP 状态
    if (e?.name === 'AbortError') {
      throw new ApiError('节点已切换，本次请求已取消', -2, 0)
    }
    throw new ApiError(`网络请求失败: ${e?.message || e}`, -1, 0)
  } finally {
    unregister()
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

  const ctrl = new AbortController()
  const unregister = registerAbort(ctrl)
  let res: Response
  try {
    res = await fetch(url, { method: 'POST', headers, body: bytes, signal: ctrl.signal })
  } catch (e: any) {
    const msg = e?.name === 'AbortError' ? '节点已切换，本次分片已取消' : String(e?.message || e)
    return { code: -1, message: msg, data: undefined }
  } finally {
    unregister()
  }
  const text = await res.text()
  try {
    return JSON.parse(text) as ApiEnvelope<T>
  } catch {
    return { code: -1, message: `响应解析失败: ${text.slice(0, 200)}`, data: undefined }
  }
}

/**
 * 带上传进度的裸字节 POST（XHR 实现）。返回完整响应信封，不 throw。
 *
 * 为什么不用 fetch：fetch 拿不到**上传**进度（只能读响应流），整包上传的场景
 * （粘贴快传）就只能干等，大文件时用户完全不知道传到哪了。XHR 的 upload.onprogress
 * 由浏览器按实际发出的字节数回调，粒度约几十毫秒，测速也准。
 *
 * body 直接传 Blob/File：浏览器会边读边发，不必像 httpPostRawBytes 那样先
 * arrayBuffer() 把整个文件读进内存——快传允许到 GB 级，那样会把内存吃爆。
 *
 * 和 fetch 版本一样登记到 net.ts 的在途集合，节点切换时会被一并中止。
 */
export function httpPostBlob<T>(
  path: string,
  params: Record<string, string>,
  body: Blob,
  onProgress?: (sent: number, total: number) => void,
): Promise<ApiEnvelope<T>> {
  const url = `${getServerUrl()}/v1${path}?${new URLSearchParams(params).toString()}`
  const token = getToken()

  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', url)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    if (token) xhr.setRequestHeader('Token', token)

    // 节点切换时 net.ts 会 abort 这个 controller，转发给 XHR
    const ctrl = new AbortController()
    const unregister = registerAbort(ctrl)
    ctrl.signal.addEventListener('abort', () => xhr.abort())

    const settle = (env: ApiEnvelope<T>) => {
      unregister()
      resolve(env)
    }

    if (onProgress) {
      // total 用 body.size 而不是 e.total：少数环境下 lengthComputable 为 false
      xhr.upload.onprogress = (e) => onProgress(e.loaded, body.size)
    }

    xhr.onload = () => {
      const text = xhr.responseText
      try {
        settle(JSON.parse(text) as ApiEnvelope<T>)
      } catch {
        settle({ code: -1, message: `响应解析失败 (HTTP ${xhr.status}): ${text.slice(0, 200)}`, data: undefined })
      }
    }
    xhr.onerror = () => settle({ code: -1, message: '网络请求失败', data: undefined })
    xhr.ontimeout = () => settle({ code: -1, message: '请求超时', data: undefined })
    xhr.onabort = () => settle({ code: -2, message: '节点已切换，本次上传已取消', data: undefined })

    xhr.send(body)
  })
}
