package com.sunyuanling.filesync.graph

import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.sunyuanling.filesync.ui.screen.permission.PermissionScreen
import com.sunyuanling.filesync.ui.screen.person.LoginScreen
import com.sunyuanling.filesync.router.HomeDestination
import com.sunyuanling.filesync.router.LoginDestination
import com.sunyuanling.filesync.router.PermissionDestination
import com.sunyuanling.filesync.ui.components.serverSetting.ConfigManager

fun NavGraphBuilder.authGraph(navController: NavHostController) {
    composable<PermissionDestination> {
        PermissionScreen(
            onPermissionsGranted = {
                ConfigManager.init()
                navController.navigate(HomeDestination) {
                    popUpTo(0) { inclusive = true }
                }
            }
        )
    }
    composable<LoginDestination> {
        LoginScreen(navController = navController)
    }
}