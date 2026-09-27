// router/index.ts
import {createRouter, createWebHashHistory} from "vue-router"
import {isTauri} from "@tauri-apps/api/core"
import {verify} from "@/api/user/userApi"

export const router = createRouter({
    history: createWebHashHistory(),
    routes: [
        {
            path: "/",
            name: "Home",
            component: () => import("../competent/home.vue"),
            redirect: "/dashboard",
            children: [
                {
                    path: "dashboard",
                    name: "Dashboard",
                    component: () => import("../views/Dashboard.vue")
                },
                {
                    path: "file/list",
                    name: "FileList",
                    component: () => import("../views/file/List.vue")
                },
                {
                    path: "file/upload",
                    name: "FileUpload",
                    component: () => import("../views/file/Upload.vue")
                },
                {
                    path: "file/catalog",
                    name: "Catalog",
                    component: () => import("../views/catalog/ViewCatalog.vue")
                },
                {
                    path: "file/share",
                    name: "FileShareManage",
                    component: () => import("../views/share/ShareManage.vue")
                },
                {
                    path: "file/quick-share",
                    name: "QuickShare",
                    component: () => import("../views/share/QuickPaste.vue")
                },
                {
                    path: "clipboard",
                    name: "ClipboardSync",
                    component: () => import("../views/clipboard/ClipboardSync.vue")
                },
                {
                    path: "monitor/system",
                    name: "MonitorSystem",
                    component: () => import("../views/monitor/System.vue")
                },
                {
                    path: "monitor/network",
                    name: "MonitorNetwork",
                    component: () => import("../views/monitor/Network.vue")
                },
                {
                    path: "monitor/cache",
                    name: "MonitorCacheManage",
                    component: () => import("../views/monitor/CacheManage.vue")
                },
                {
                    path: "sync/watch",
                    name: "SyncWatch",
                    component: () => import("../views/sync/SyncWatch.vue")
                },
                {
                    path: "sync/manage",
                    name: "SyncManage",
                    component: () => import("../views/sync/SyncManage.vue")
                },
                {
                    path: "update/manage",
                    name: "AppUpdateManage",
                    component: () => import("../views/update/AppUpdateManage.vue")
                },
                {
                    path: "transfers",
                    name: "Transfers",
                    component: () => import("../views/transfer/TransferList.vue")
                },
                {
                    path: "person/center",
                    name: "PersonCenter",
                    component: () => import("../views/person/PersonalCenter.vue")
                },
                {
                    path: "person/edit",
                    name: "PersonEdit",
                    component: () => import("../views/person/EditProfile.vue")
                },
                {
                    path: "person/password",
                    name: "PersonPassword",
                    component: () => import("../views/person/ChangePassword.vue")
                },
                {
                    path: "person/quick-share",
                    name: "PersonQuickShareSettings",
                    component: () => import("../views/person/QuickShareSettings.vue")
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

router.beforeEach(async (to, _from, next) => {
    const token = localStorage.getItem("token")
    const publicPages = ['/login', '/register', '/reset']

    if (publicPages.includes(to.path)) {
        if (token) {
            next('/')
        } else {
            next()
        }
        return
    }

    if (!token) {
        next('/login')
        return
    }

    if (isTauri() && !restoredThisSession) {
        try {
            await verify()
            restoredThisSession = true
        } catch {
            // token 已失效或 Rust 校验不通过：清掉本地缓存，回登录页重新来
            localStorage.removeItem('token')
            next('/login')
            return
        }
    }

    next()
})

router.onError(async (error) => {
    console.error("路由错误:", error)
    await router.push("/")
})
