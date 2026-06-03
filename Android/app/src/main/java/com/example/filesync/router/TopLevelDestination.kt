package com.example.filesync.router

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.ui.graphics.vector.ImageVector
import kotlin.reflect.KClass

enum class TopLevelDestination(
    val route: KClass<*>,
    val label: String,
    val icon: ImageVector
) {
    HOME(HomeDestination::class, "主页", Icons.Default.Home),
    FILES(FilesDestination::class, "文件", Icons.Default.Folder),
    MONITOR(MonitorDestination::class, "监控", Icons.Default.Monitor),
    PERSONAL(PersonalDestination::class, "个人中心", Icons.Default.Person)
}