// composeables/menu-config.ts
import {useRouter} from "vue-router";
import type {UserInfo} from "@/api/user/userTypes";

function isAdmin(): boolean {
    const saved = localStorage.getItem("userInfo")
    if (!saved) return false
    try {
        return (JSON.parse(saved) as UserInfo).role === "admin"
    } catch {
        return false
    }
}

export const useMenuConfig=()=>{
    const router=useRouter()
    const monitorChildren = [
        {
            name: "系统状态",
            path: "/monitor/system",
            icon: "setting",
            click: () => router.push("/monitor/system"),
        },
        {
            name: "网络监控",
            path: "/monitor/network",
            icon: "setting",
            click: () => router.push("/monitor/network"),
        },
    ]
    // 缓存管理涉及跨用户查看/清理粘贴快传缓存，只给管理员看得见这个入口
    // （后端 /admin/quick-share 也会再校验一遍角色，这里只是不给非管理员显示）
    if (isAdmin()) {
        monitorChildren.push({
            name: "缓存管理",
            path: "/monitor/cache",
            icon: "setting",
            click: () => router.push("/monitor/cache"),
        })
    }
    const menuConfig=[{
        name: "首页",
        path: "/",
        icon: "home",
        click: () => router.push("/"),
    },
        {
            name: "文件管理",
            path: "/file",
            icon: "file",
            click: () => router.push("/file"),
            children: [
                {
                    name: "文件列表",
                    path: "/file/list",
                    icon: "file-list",
                    click: () => router.push("/file/list"),
                },
                {
                    name: "上传管理",
                    path: "/file/upload",
                    icon: "file-list",
                    click: () => router.push("/file/upload"),
                },
                {
                    name: "分享管理",
                    path: "/file/share",
                    icon: "file-list",
                    click: () => router.push("/file/share"),
                },
                {
                    name: "快速分享",
                    path: "/file/quick-share",
                    icon: "file-list",
                    click: () => router.push("/file/quick-share"),
                }
            ]
        },
        {
            name: "文件同步",
            path: "/sync",
            icon: "sync",
            click: () => router.push("/sync/manage"),
            children: [
                {
                    name: "同步管理",
                    path: "/sync/manage",
                    icon: "sync",
                    click: () => router.push("/sync/manage"),
                },
                {
                    name: "目录监听",
                    path: "/sync/watch",
                    icon: "sync",
                    click: () => router.push("/sync/watch"),
                }
            ]
        },
        {
            name: "应用更新",
            path: "/update",
            icon: "setting",
            click: () => router.push("/update/manage"),
            children: [
                {
                    name: "版本发布",
                    path: "/update/manage",
                    icon: "setting",
                    click: () => router.push("/update/manage"),
                }
            ]
        },
        {
            name: "剪贴板同步",
            path: "/clipboard",
            icon: "sync",
            click: () => router.push("/clipboard"),
        },
        {
            name: "系统监控",
            path: "/monitor",
            icon: "setting",
            click: () => router.push("/monitor"),
            children: monitorChildren
        }]
    return {
        menuConfig,
    }
}
