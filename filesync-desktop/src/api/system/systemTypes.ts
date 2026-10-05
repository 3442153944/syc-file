// api/system/systemTypes.ts
// 初始化 / 路由下发 / 访客 / 级别 的类型。字段名与后端 json tag 逐一对齐（internal/system）。

/** 权限级别：0 游客 / 1 用户 / 2 管理员 / 3 超级管理员 */
export type Level = 0 | 1 | 2 | 3

/** GET /system/status（免登录） */
export interface ServerStatus {
  initialized: boolean
  registration_open: boolean
  version: string
  mode: string
  name: string
}

export interface InitParams {
  setup_code: string
  username: string
  password: string
  email?: string
  allow_register: boolean
}

/** 服务器下发的一条路由。Component 是客户端「页面注册表」的 key，空 = 菜单分组。 */
export interface RouteItem {
  code: string
  parent_code: string
  path: string
  name: string
  component: string
  title: string
  icon: string
  sort: number
  hidden: boolean
}

export interface MyRoutes {
  logged_in: boolean
  level: number
  version: string
  routes: RouteItem[]
}

/** GET /admin/routes：完整路由（含管理字段） */
export interface AdminRoute extends RouteItem {
  id: number
  min_level: number
  perm: string
  enabled: boolean
  /** 来自服务端内置目录：不能删除，也不能改路径 / 组件 / 父级，只能停用或调整展示 */
  builtin: boolean
}

/** 新增路由的请求体。component 为空 = 菜单分组 */
export interface CreateRouteBody {
  code: string
  title: string
  path?: string
  name?: string
  component?: string
  parent_code?: string
  icon?: string
  sort?: number
  hidden?: boolean
  min_level?: number
  perm?: string
  enabled?: boolean
}

/** 游客接口分组（决定游客拿到某页面后额外放行哪些接口） */
export interface RoutePerm {
  key: string
  label: string
}

export interface GuestRow {
  id: number
  username: string
  status: number
  expires_at: string | null
  expired: boolean
  created_by: number | null
  created_at: string
  last_login: string | null
  route_codes: string[]
}

/** 创建访客的响应：明文密码只返回这一次 */
export interface GuestCreated {
  id: number
  username: string
  password: string
  expires_at: string
  route_codes: string[]
}
