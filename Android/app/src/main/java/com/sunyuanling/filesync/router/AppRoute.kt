// router/AppRoute.kt
package com.sunyuanling.filesync.router

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.ui.graphics.vector.ImageVector

/**
 * 底部导航项配置
 * 必须定义在 AppRoute 外部，避免初始化顺序问题
 */
data class BottomNavItem(
    val route: AppRoute,
    val label: String,
    val icon: ImageVector
)

/**
 * 应用路由定义
 * 分为三类：主 Tab、详情页、特殊页面
 */
sealed class AppRoute(val route: String) {

    // ==================== 主 Tab 页面（底部导航） ====================
    data object Home : AppRoute("home")
    data object Files : AppRoute("files")
    data object Monitor : AppRoute("monitor")
    data object Personal : AppRoute("personal")

    // ==================== 详情页面（全屏，可从多处打开） ====================
    data object Transfer : AppRoute("transfer")

    data object FileDetail : AppRoute("file_detail/{fileId}") {
        const val ARG_FILE_ID = "fileId"
        fun createRoute(fileId: String) = "file_detail/$fileId"
    }

    data object FileUpload : AppRoute("file_upload")

    data object FileSearch : AppRoute("file_search")

    // ==================== 设置相关页面 ====================
    data object Settings : AppRoute("settings")

    data object ServerSettings : AppRoute("settings/server")

    data object SyncSettings : AppRoute("settings/sync")

    data object About : AppRoute("about")

    // ==================== 特殊页面 ====================
    data object Permission : AppRoute("permission")

    data object Login : AppRoute("login")

    //服务器设置
    data object ServerSetting: AppRoute("serverSetting")

    companion object {
        /**
         * 底部导航栏路由列表
         */
        val bottomNavRoutes: List<BottomNavItem>
            get() = listOf(
                BottomNavItem(Home, "主页", Icons.Default.Home),
                BottomNavItem(Files, "文件", Icons.Default.Folder),
                BottomNavItem(Monitor, "监控", Icons.Default.Monitor),
                BottomNavItem(Personal, "个人中心", Icons.Default.Person)
            )

        /**
         * 主 Tab 路由集合（用于判断）
         */
        private val mainTabRoutes: Set<String>
            get() = bottomNavRoutes.map { it.route.route }.toSet()

        /**
         * 判断是否为主 Tab 路由
         */
        fun isMainTab(route: String?): Boolean {
            return route in mainTabRoutes
        }

        /**
         * 不显示底部导航的页面
         */
        /**
         * 不显示底部导航的页面
         */
        private val hideBottomNavRoutes = setOf("permission", "login")

        /**
         * 判断是否应该显示底部导航
         */
        fun shouldShowBottomNav(route: String?): Boolean {
            return route !in hideBottomNavRoutes
        }
    }
}