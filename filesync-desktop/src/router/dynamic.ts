// router/dynamic.ts
// 服务器下发路由的客户端侧：页面注册表 + 动态注册 + 菜单树。
//
// 客户端只内置登录 / 注册 / 重置 / 初始化等基础路由，其余页面的路由全部来自 GET /v1/routes。
// 页面代码仍打在客户端里：服务器下发的 `component` 只是下面「页面注册表」的 key（相对 src/ 的 .vue 路径，
// 如 views/file/List.vue）。服务器决定「有哪些入口、顺序、标题、谁能进」，客户端找不到对应组件的条目会被忽略
// ——所以老客户端遇到新条目不会崩，也意味着新增页面必须随客户端发版。
import type { Router } from 'vue-router'
import type { RouteItem } from '@/api/system/systemTypes'

// 只扫 views/：基础路由（登录 / 初始化等）在 competent/，不会被服务器下发
const modules = import.meta.glob('../views/**/*.vue')

type Loader = () => Promise<unknown>
const registry: Record<string, Loader> = {}
for (const [k, v] of Object.entries(modules)) {
  registry[k.replace(/^\.\.\//, '')] = v as Loader
}

export const hasComponent = (component: string): boolean => !!registry[component]

/** 动态路由挂在 Home 布局下（Home 的 path 是 "/"，子路由写相对路径） */
export const HOME_ROUTE_NAME = 'Home'

let added: string[] = []

export function removeDynamicRoutes(router: Router): void {
  for (const name of added) {
    if (router.hasRoute(name)) router.removeRoute(name)
  }
  added = []
}

/** 用服务器下发的路由表替换当前动态路由。返回实际注册成功的条数。 */
export function registerRoutes(router: Router, list: RouteItem[]): number {
  removeDynamicRoutes(router)
  for (const r of list) {
    if (!r.component) continue // 菜单分组，没有页面
    const loader = registry[r.component]
    if (!loader) {
      console.warn(`[routes] 客户端没有页面组件 ${r.component}（路由 ${r.code}），已忽略`)
      continue
    }
    const name = r.name || `route:${r.code}`
    if (router.hasRoute(name)) {
      console.warn(`[routes] 路由名重复 ${name}（${r.code}），已忽略`)
      continue
    }
    router.addRoute(HOME_ROUTE_NAME, {
      path: r.path.replace(/^\//, ''),
      name,
      component: loader as never,
      meta: { code: r.code, title: r.title, hidden: r.hidden },
    })
    added.push(name)
  }
  return added.length
}

export interface MenuNode {
  /** 菜单项唯一 key：页面用 path（点击即跳转），分组用 dir:<code> */
  key: string
  title: string
  icon: string
  path?: string
  children?: MenuNode[]
}

/** 平铺路由 → 菜单树：隐藏项、客户端没有的页面不进菜单；没有可见子项的分组整个丢掉。 */
export function buildMenu(list: RouteItem[]): MenuNode[] {
  const byCode = new Map<string, MenuNode>()
  const roots: MenuNode[] = []

  // 先建节点（服务器已按 sort 排好序，保持原顺序）
  const order: RouteItem[] = []
  for (const r of list) {
    if (r.component) {
      if (r.hidden || !hasComponent(r.component)) continue
      byCode.set(r.code, { key: r.path, title: r.title, icon: r.icon, path: r.path })
    } else {
      byCode.set(r.code, { key: `dir:${r.code}`, title: r.title, icon: r.icon, children: [] })
    }
    order.push(r)
  }
  for (const r of order) {
    const node = byCode.get(r.code)!
    const parent = r.parent_code ? byCode.get(r.parent_code) : undefined
    if (parent?.children) parent.children.push(node)
    else if (!r.parent_code || !parent) roots.push(node)
  }

  const prune = (nodes: MenuNode[]): MenuNode[] =>
    nodes
      .map((n) => (n.children ? { ...n, children: prune(n.children) } : n))
      .filter((n) => !n.children || n.children.length > 0)
  return prune(roots)
}

/** 登录后默认落地页：优先首页（dashboard），否则第一个客户端有组件的页面。没有则返回空串。 */
export function defaultLandingPath(list: RouteItem[]): string {
  const usable = list.filter((r) => r.component && hasComponent(r.component))
  const home = usable.find((r) => r.code === 'dashboard' && !r.hidden)
  if (home) return home.path
  const first = usable.find((r) => !r.hidden) ?? usable[0]
  return first ? first.path : ''
}
