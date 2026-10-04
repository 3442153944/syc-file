// router/index.ts
// 路由分两类：
//   - 基础路由（本文件内置）：登录 / 注册 / 重置密码 / 初始化向导 / Home 布局壳 / 兜底页，不依赖服务器就能用；
//   - 业务路由：全部由服务器下发（GET /v1/routes），登录后由守卫动态注册到 Home 下，见 router/dynamic.ts。
import {createRouter, createWebHashHistory} from "vue-router"
import {isTauri} from "@tauri-apps/api/core"
import {verify} from "@/api/user/userApi"
import {pinia} from "@/store/useStore"
import {useRouteStore} from "@/store/useRouteStore"

export const router = createRouter({
    history: createWebHashHistory(),
    routes: [
        {
            path: "/",
            name: "Home",
            component: () => import("../competent/home.vue"),
            children: [
                // 没有任何可访问页面 / 路由表加载失败时的落地页（静态注册，保证总有地方可去）
                {
                    path: "empty",
                    name: "Empty",
                    component: () => import("../competent/EmptyPage.vue")
                }
            ]
        },
        {
            path: "/login",
            name: "Login",
            component: () => import("../competent/login/login.vue")
        },
        {
            path: "/register",
            name: "Register",
            component: () => import("../competent/register/register.vue")
        },
        {
            path: "/reset",
            name: "ResetPassword",
            component: () => import("../competent/resetPassword/resetPassword.vue")
        },
        {
            path: "/init",
            name: "Init",
            component: () => import("../competent/init/InitWizard.vue")
        },
        {
            path: "/:pathMatch(.*)*",
            name: "NotFound",
            redirect: "/"
        }
    ]
})

// Tauri 下 token 只在前端 localStorage 持久，Rust 侧的运行期状态（SyncConfig.token）
// 故意不落盘，每次冷启动都是空的——用户完全退出重开后，路由守卫看 localStorage
// 有 token 就放行进了首页，但 Rust 还没有这个 token，第一批走 invoke 的接口
// （get_available_disks 等）打到后端就是匿名请求，报 401 未登录。
// 这里在本次会话第一次进受保护页时，把 token 交还给 Rust 并顺带校验一次
//（userApi.verify 内部调 restore_token），之后的导航不用再重复这一步。
let restoredThisSession = false

const publicPages = ['/login', '/register', '/reset']

function clearSession() {
    localStorage.removeItem('token')
    localStorage.removeItem('userInfo')
}

router.beforeEach(async (to) => {
    const rs = useRouteStore(pinia)
    const token = localStorage.getItem("token")

    // 1. 服务器是否已初始化：没有就一律引导去初始化向导（服务器不可达时 status 为 null，不拦，交给登录页报错）
    const status = await rs.ensureStatus()
    if (status && !status.initialized) {
        return to.path === '/init' ? true : '/init'
    }
    if (to.path === '/init') {
        return token ? '/' : '/login'
    }

    // 2. 公开页面
    if (publicPages.includes(to.path)) {
        if (token) return '/'
        // 注册已关闭：不让进注册页（登录页也会隐藏入口，这里兜直接输地址的情况）
        if (to.path === '/register' && status && !status.registration_open) return '/login'
        return true
    }

    if (!token) return '/login'

    if (isTauri() && !restoredThisSession) {
        try {
            await verify()
            restoredThisSession = true
        } catch {
            // token 已失效或 Rust 校验不通过：清掉本地缓存，回登录页重新来
            clearSession()
            return '/login'
        }
    }

    // 3. 本次会话第一次进受保护页：拉服务器下发的路由表并动态注册，然后重新解析一次当前地址
    //    （刷新页面直接停在 #/file/list 时，这条路由此刻还不存在，得注册完再匹配）
    if (!rs.loaded) {
        const ok = await rs.load(router)
        if (!ok) {
            if (rs.loadError === 'unauthorized') {
                clearSession()
                return '/login'
            }
            return to.path === '/empty' ? true : '/empty'
        }
        return {path: to.fullPath, replace: true}
    }

    // 4. 根路径：落到第一个可访问页面
    if (to.path === '/') {
        return rs.landingPath || '/empty'
    }
    return true
})

router.onError(async (error) => {
    console.error("路由错误:", error)
    await router.push("/")
})
