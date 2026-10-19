/** 路由版本相同仍要核对内容，让客户端能从曾被错误处理的菜单响应中自动恢复。 */
interface RouteSnapshot {
  version: string
  level: number
  routes: readonly unknown[]
}

export function shouldApplyRouteSnapshot(current: RouteSnapshot, incoming: RouteSnapshot, fromCache = false): boolean {
  return fromCache || current.version !== incoming.version || current.level !== incoming.level
    || JSON.stringify(current.routes) !== JSON.stringify(incoming.routes)
}
