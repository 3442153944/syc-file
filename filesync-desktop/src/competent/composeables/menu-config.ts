// composeables/menu-config.ts
import {useRouter} from "vue-router";

export const useMenuConfig=()=>{
    const router=useRouter()
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
                }
            ]
        },
        {
            name: "同步监听",
            path: "/sync",
            icon: "sync",
            click: () => router.push("/sync"),
            children: [
                {
                    name: "目录监听",
                    path: "/sync/watch",
                    icon: "sync",
                    click: () => router.push("/sync/watch"),
                }
            ]
        },
        {
            name: "系统监控",
            path: "/monitor",
            icon: "setting",
            click: () => router.push("/monitor"),
            children: [
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
                }
            ]
        }]
    return {
        menuConfig,
    }
}
