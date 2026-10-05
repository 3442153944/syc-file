// api/system/systemApi.ts
import { request } from '../request'
import type {
  ServerStatus, InitParams, MyRoutes, AdminRoute, RouteItem, GuestRow, GuestCreated,
  CreateRouteBody, RoutePerm,
} from './systemTypes'

// ── 初始化（免登录）──────────────────────────────────────────
export const getServerStatus = () => request<ServerStatus>('GET', '/system/status')
export const initServer = (body: InitParams) =>
  request<{ user_id: number; username: string }>('POST', '/system/init', { body })

// ── 路由下发 ─────────────────────────────────────────────────
/** 当前登录者可见的路由；token 无效 / 访客失效时 logged_in=false。 */
export const getMyRoutes = () => request<MyRoutes>('GET', '/routes')

// ── 路由管理（仅超级管理员）──────────────────────────────────
export const adminListRoutes = () => request<AdminRoute[]>('GET', '/admin/routes')
/** path / component / parent_code / name / perm 只有自定义路由能改，内置路由改这些会被服务端拒绝。 */
export const adminUpdateRoute = (
  id: number,
  patch: {
    title?: string; icon?: string; sort?: number; hidden?: boolean; min_level?: number; enabled?: boolean
    path?: string; component?: string; parent_code?: string; name?: string; perm?: string
  },
) => request<null>('PUT', `/admin/routes/${id}`, { body: patch })
export const adminCreateRoute = (body: CreateRouteBody) => request<AdminRoute>('POST', '/admin/routes', { body })
/** 只能删自定义路由，且其下不能还有子路由 */
export const adminDeleteRoute = (id: number) => request<null>('DELETE', `/admin/routes/${id}`)
export const adminRoutePerms = () => request<RoutePerm[]>('GET', '/admin/route-perms')

// ── 访客（管理员及以上）──────────────────────────────────────
export const listGuestRoutes = () => request<RouteItem[]>('GET', '/admin/guest-routes')
export const listGuests = () => request<GuestRow[]>('GET', '/admin/guests')
export const createGuest = (body: { expire_hours: number; route_codes: string[] }) =>
  request<GuestCreated>('POST', '/admin/guests', { body })
export const updateGuest = (
  id: number,
  patch: { expire_hours?: number; route_codes?: string[]; status?: number; reset_password?: boolean },
) => request<{ password?: string }>('PUT', `/admin/guests/${id}`, { body: patch })
export const deleteGuest = (id: number) => request<null>('DELETE', `/admin/guests/${id}`)

// ── 级别与注册开关 ───────────────────────────────────────────
/** 仅超级管理员；level: 1 用户 / 2 管理员。超级管理员全系统只有一个，不能再设置。 */
export const setUserLevel = (id: number, level: number) =>
  request<null>('PUT', `/admin/users/${id}/level`, { body: { level } })

/**
 * 管理员创建账号（自助注册关闭时的唯一途径）。level 1 用户（管理员及以上可建）/ 2 管理员（仅超管可建）。
 * password 留空由服务端生成，并只在本次响应里返回一次。
 */
export const createUser = (body: { username: string; password?: string; email?: string; level: number }) =>
  request<{ id: number; username: string; level: number; password?: string }>('POST', '/admin/users', { body })
export const getSettings = () => request<{ allow_register: boolean }>('GET', '/admin/settings')
export const setRegisterOpen = (allow: boolean) =>
  request<{ allow_register: boolean }>('PUT', '/admin/settings/register', { body: { allow } })
