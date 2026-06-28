// router/index.ts
import {createRouter, createWebHashHistory} from "vue-router"

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
                    path: "sync/watch",
                    name: "SyncWatch",
                    component: () => import("../views/sync/SyncWatch.vue")
                },
                {
                    path: "sync/manage",
                    name: "SyncManage",
                    component: () => import("../views/sync/SyncManage.vue")
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

    next()
})

router.onError(async (error) => {
    console.error("路由错误:", error)
    await router.push("/")
})
