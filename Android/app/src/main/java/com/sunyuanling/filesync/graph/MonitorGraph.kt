package com.sunyuanling.filesync.graph

import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.sunyuanling.filesync.router.MonitorListDestination
import com.sunyuanling.filesync.ui.screen.monitoring.DevicesList

fun NavGraphBuilder.monitorGraph(navController: NavHostController) {
    composable<MonitorListDestination> {
        DevicesList(navController = navController)
    }
}
