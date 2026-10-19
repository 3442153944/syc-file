// api/admin/dshApi.ts
// DSH（DeepSeek Harness）访问令牌的管理接口。
//
// 令牌明文只在创建时返回一次；列表只给状态和备注。
import { request, clean } from '../request'
import type { Paged } from './adminTypes'

export type DshTokenKind = 'one_time' | 'long_lived'
export type DshTokenStatus = 'active' | 'used' | 'expired' | 'revoked'

export interface DshToken {
  id: number
  kind: DshTokenKind
  label: string
  created_by: number
  expires_at: string
  used_at?: string | null
  last_used_at?: string | null
  revoked_at?: string | null
  created_at: string
  status: DshTokenStatus
}

export interface DshTokenLink {
  base: string
  url: string
}

export interface CreateDshTokenResult {
  id: number
  token: string
  kind: DshTokenKind
  label: string
  expires_at: string
  links: DshTokenLink[]
}

export const listDshTokens = (p: { page?: number; page_size?: number } = {}) =>
  request<Paged<DshToken>>('GET', '/admin/dsh/tokens', { query: clean(p) })

export const createDshToken = (body: {
  kind: DshTokenKind
  label?: string
  expires_hours?: number
}) => request<CreateDshTokenResult>('POST', '/admin/dsh/tokens', { body })

export const revokeDshToken = (id: number) =>
  request<null>('DELETE', `/admin/dsh/tokens/${id}`)
