// store/useRouteStore.ts
// 服务器状态（是否已初始化 / 是否开放注册）+ 服务器下发的路由表。
//
// 路由表加载策略：
//   1. 登录后 / 应用启动时，由路由守卫调 load() 拉 GET /v1/routes，注册成动态路由；
//   2. 拉取失败（服务器暂时不可达）时，回退到上次成功缓存（同一用户才用，防止串号）；
//   3. refresh() 定时重拉，版本号（version）变了才重新注册——管理员改了路由表，在线客户端几分钟内跟上，不必重新登录。
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import type { Router } from 'vue-router'
import { getServerStatus, getMyRoutes } from '@/api/system/systemApi'
import type { ServerStatus, RouteItem } from '@/api/system/systemTypes'
import { baseUrl } from '@/api/net'
import { registerRoutes, removeDynamicRoutes, buildMenu, defaultLandingPath } from '@/router/dynamic'
import { currentUserId } from '@/utils/level'

const CACHE_KEY = 'routes_cache'
/** 状态探测失败后多久内不再重试，避免服务器不可达时每次导航都卡一次超时 */
const STATUS_RETRY_MS = 10_000

interface RoutesCache {
  userId: number | null
  version: string
  level: number
  routes: RouteItem[]
}

function readCache(): RoutesCache | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY)
    if (!raw) return null
    const c = JSON.parse(raw) as RoutesCache
    // 只认同一用户的缓存：换账号后用上一个人的路由表会让界面和权限对不上
    return c.userId === currentUserId() ? c : null
  } catch {
    return null
  }
}

export const useRouteStore = defineStore('routes', () => {
  const status = ref<ServerStatus | null>(null)
  const statusKey = ref('')
  let statusFailAt = 0

  const routes = ref<RouteItem[]>([])
  const version = ref('')
  const level = ref(0)
  const loaded = ref(false)
  const fromCache = ref(false)
  /** 'unauthorized' = token 失效 / 访客账号失效；其它为网络类错误文案 */
  const loadError = ref('')

  const menu = computed(() => buildMenu(routes.value))
  const landingPath = computed(() => defaultLandingPath(routes.value))

  /** 取服务器状态（按当前节点缓存；节点切换后自动重取）。服务器不可达时返回 null，不阻塞后续流程。 */
  async function ensureStatus(force = false): Promise<ServerStatus | null> {
    const key = baseUrl()
    if (!force && status.value && statusKey.value === key) return status.value
    if (!force && Date.now() - statusFailAt < STATUS_RETRY_MS && statusKey.value === key) return null
    try {
      status.value = await getServerStatus()
      statusKey.value = key
      statusFailAt = 0
    } catch (e) {
      console.warn('[status] 获取服务器状态失败', e)
      status.value = null
      statusKey.value = key
      statusFailAt = Date.now()
    }
    return status.value
  }

  function markInitialized() {
    if (status.value) status.value = { ...status.value, initialized: true }
  }

  function apply(router: Router, list: RouteItem[], ver: string, lv: number) {
    registerRoutes(router, list)
    routes.value = list
    version.value = ver
    level.value = lv
    loaded.value = true
  }

  /** 首次加载。成功（含缓存回退）返回 true；token 失效或无缓存时返回 false，原因见 loadError。 */
  async function load(router: Router): Promise<boolean> {
    loadError.value = ''
    try {
      const data = await getMyRoutes()
      if (!data.logged_in) {
        loadError.value = 'unauthorized'
        return false
      }
      apply(router, data.routes, data.version, data.level)
      fromCache.value = false
      try {
        const cache: RoutesCache = {
          userId: currentUserId(), version: data.version, level: data.level, routes: data.routes,
        }
        localStorage.setItem(CACHE_KEY, JSON.stringify(cache))
      } catch { /* 缓存写失败不影响使用 */ }
      return true
    } catch (e) {
      const cached = readCache()
      if (cached) {
        console.warn('[routes] 拉取失败，使用本地缓存的路由表', e)
        apply(router, cached.routes, cached.version, cached.level)
        fromCache.value = true
        return true
      }
      loadError.value = e instanceof Error ? e.message : String(e)
      return false
    }
  }

  /** 定时刷新：版本没变就什么都不做；拉取失败静默忽略（继续用现有路由表）。 */
  async function refresh(router: Router): Promise<void> {
    if (!loaded.value) return
    try {
      const data = await getMyRoutes()
      if (!data.logged_in) {
        loadError.value = 'unauthorized'
        loaded.value = false // 让下一次导航走重新加载 → 回登录
        return
      }
      if (data.version === version.value && !fromCache.value) return
      apply(router, data.routes, data.version, data.level)
      fromCache.value = false
    } catch { /* 网络抖动：保持现状 */ }
  }

  /** 退出登录 / 换账号：清掉动态路由与内存里的路由表（本地缓存保留，按 userId 隔离）。 */
  function reset(router: Router) {
    removeDynamicRoutes(router)
    routes.value = []
    version.value = ''
    level.value = 0
    loaded.value = false
    fromCache.value = false
    loadError.value = ''
  }

  return {
    status, routes, version, level, loaded, fromCache, loadError, menu, landingPath,
    ensureStatus, markInitialized, load, refresh, reset,
  }
})
