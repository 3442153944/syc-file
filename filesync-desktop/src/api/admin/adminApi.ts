// api/admin/adminApi.ts
// 管理域 + 监控 API。
//
// Tauri 模式统一走 Rust 侧的通用代理命令 `api_request`（token/服务器地址由 Rust 的 SyncConfig 提供，
// 不依赖 webview 里可能过期的 localStorage token）；Web 模式走 http.ts 的 fetch。
// 管理域接口多且形态简单，逐个包 Rust command 属于重复劳动，故用一个通用代理。
import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpGet, httpPost, httpPut, httpDelete } from '../http'
import type {
  AdminUser, DeviceRow, OperationLogRow, StorageRow, RoleWithPerms, Permission,
  SystemMetrics, NetworkMetrics, Paged,
} from './adminTypes'

/** 统一请求入口：Tauri → invoke 代理；Web → fetch。 */
async function request<T>(
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
function clean(params: Record<string, string | number | undefined | null>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') out[k] = String(v)
  }
  return out
}

// ── 监控 ──────────────────────────────────────────────────
export const getSystemMetrics = () => request<SystemMetrics>('GET', '/monitor/system')
export const getNetworkMetrics = () => request<NetworkMetrics>('GET', '/monitor/network')

// ── 用户管理 ──────────────────────────────────────────────
export const listUsers = (p: {
  keyword?: string; role?: string; status?: string; page?: number; page_size?: number
} = {}) => request<Paged<AdminUser>>('GET', '/admin/users', { query: clean(p) })

export const updateUser = (
  id: number,
  updates: { role?: string; status?: number; email?: string; phone?: string },
) => request<null>('PUT', `/admin/users/${id}`, { body: updates })

export const resetUserPassword = (id: number, newPassword: string) =>
  request<null>('POST', `/admin/users/${id}/reset-password`, { body: { new_password: newPassword } })

export const deleteUser = (id: number) => request<null>('DELETE', `/admin/users/${id}`)

// ── 设备管理 ──────────────────────────────────────────────
/** admin=true 时走管理员视角（可跨用户）；否则只看自己的设备。 */
export const listDevices = (
  p: { keyword?: string; user_id?: number; online?: string; page?: number; page_size?: number } = {},
  admin = false,
) => request<Paged<DeviceRow>>('GET', admin ? '/admin/devices' : '/devices', { query: clean(p) })

export const renameDevice = (id: number, deviceName: string) =>
  request<null>('PUT', `/devices/${id}`, { body: { device_name: deviceName } })

export const kickDevice = (id: number) => request<null>('POST', `/devices/${id}/kick`)
export const unbindDevice = (id: number) => request<null>('DELETE', `/devices/${id}`)

// ── 操作日志 ──────────────────────────────────────────────
export const listLogs = (p: {
  user_id?: number; module?: string; type?: string; status?: string
  keyword?: string; start?: number; end?: number; page?: number; page_size?: number
} = {}) => request<Paged<OperationLogRow>>('GET', '/admin/logs', { query: clean(p) })

export const listLogModules = () => request<string[]>('GET', '/admin/logs/modules')

/** before 为 Unix 秒，只清理该时间之前的日志（服务端拒绝不带 before 的全清）。 */
export const clearLogs = (before: number) =>
  request<{ deleted: number }>('DELETE', '/admin/logs', { query: { before: String(before) } })

// ── 存储配额 ──────────────────────────────────────────────
export const listStorage = (p: { page?: number; page_size?: number } = {}) =>
  request<Paged<StorageRow>>('GET', '/admin/storage', { query: clean(p) })

export const myStorage = () =>
  request<{ config: StorageRow; used_percent: number }>('GET', '/storage/mine')

export const updateQuota = (userId: number, totalQuota: number) =>
  request<null>('PUT', `/admin/storage/${userId}`, { body: { total_quota: totalQuota } })

export const recalcStorage = (userId: number) =>
  request<{ used_quota: number; file_count: number }>('POST', `/admin/storage/${userId}/recalc`)

// ── 角色权限 ──────────────────────────────────────────────
export const listRoles = () => request<RoleWithPerms[]>('GET', '/admin/roles')
export const createRole = (body: { role_code: string; role_name: string; description?: string }) =>
  request<RoleWithPerms>('POST', '/admin/roles', { body })
export const updateRole = (
  id: number,
  body: { role_name?: string; description?: string; status?: number; permission_ids?: number[] },
) => request<null>('PUT', `/admin/roles/${id}`, { body })
export const deleteRole = (id: number) => request<null>('DELETE', `/admin/roles/${id}`)

export const listPermissions = () => request<Permission[]>('GET', '/admin/permissions')
export const createPermission = (body: {
  permission_code: string; permission_name: string; permission_type?: string
  description?: string; sort_order?: number
}) => request<Permission>('POST', '/admin/permissions', { body })
export const deletePermission = (id: number) => request<null>('DELETE', `/admin/permissions/${id}`)

export const getUserRoles = (userId: number) =>
  request<RoleWithPerms[]>('GET', `/admin/users/${userId}/roles`)
export const assignUserRoles = (userId: number, roleIds: number[]) =>
  request<{ role: string; note: string }>('PUT', `/admin/users/${userId}/roles`, {
    body: { role_ids: roleIds },
  })
