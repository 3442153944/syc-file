// api/admin/vpsApi.ts
// VPS 节点面板入口：每条节点自带随机入口路径 + 访问秘钥，点「打开」现签入口地址。
//
// 秘钥明文只在创建 / 轮换时返回一次；列表里给的是现签好的 entry_url（含秘钥），
// 方便直接点开，但不要外传——拿到它等于拿到面板入口。
import { request, clean } from '../request'
import type { Paged } from './adminTypes'

export type VpsStatus = 'ok' | 'fail' | 'unknown'

export interface VpsNode {
  id: number
  code: string
  title: string
  region: string
  provider: string
  host: string
  gateway_host: string
  panel_scheme: 'http' | 'https'
  panel_port: number
  panel_path: string
  gateway_port: number
  access_path: string
  sub_url: string
  sub_port: number
  remark: string
  enabled: boolean
  sort: number
  /** 现签入口地址：<gateway>/p/xxxx/?k=<秘钥> */
  entry_url: string
  last_status: VpsStatus
  last_latency_ms: number
  last_checked_at?: string | null
  last_error: string
  traffic_up: number
  traffic_down: number
  traffic_total: number
  traffic_expire?: string | null
  created_at: string
  updated_at: string
}

export interface VpsNodeInput {
  code?: string
  title: string
  region?: string
  provider?: string
  host: string
  gateway_host?: string
  panel_scheme?: 'http' | 'https'
  panel_port?: number
  panel_path?: string
  gateway_port?: number
  sub_url?: string
  sub_port?: number
  remark?: string
  sort?: number
  enabled?: boolean
}

export interface VpsCreatedResult {
  node: VpsNode
  access_key: string
  snippet?: string
}

export interface VpsSnippetResult {
  entry_url: string
  snippet: string
}

export const listVpsNodes = (p: { page?: number; page_size?: number; keyword?: string } = {}) =>
  request<Paged<VpsNode>>('GET', '/admin/vps/nodes', { query: clean(p) })

export const createVpsNode = (body: VpsNodeInput) =>
  request<VpsCreatedResult>('POST', '/admin/vps/nodes', { body })

export const updateVpsNode = (id: number, body: VpsNodeInput) =>
  request<VpsNode>('PUT', `/admin/vps/nodes/${id}`, { body })

export const deleteVpsNode = (id: number) =>
  request<null>('DELETE', `/admin/vps/nodes/${id}`)

/** 重新生成入口路径与秘钥；旧入口立即失效，节点上的 nginx 片段要同步替换。 */
export const rotateVpsNode = (id: number) =>
  request<VpsCreatedResult>('POST', `/admin/vps/nodes/${id}/rotate`)

/** 探活：请求入口地址，正确秘钥应得到 302；顺带刷新订阅流量。 */
export const checkVpsNode = (id: number) =>
  request<VpsNode>('POST', `/admin/vps/nodes/${id}/check`)

/** 取该节点要落到 nginx 的配置片段。 */
export const vpsNodeSnippet = (id: number) =>
  request<VpsSnippetResult>('GET', `/admin/vps/nodes/${id}/snippet`)

/** 一次性导出所有启用节点的片段（拼成一段文本）。 */
export const vpsAllSnippets = () =>
  request<{ text: string }>('GET', '/admin/vps/snippets')
