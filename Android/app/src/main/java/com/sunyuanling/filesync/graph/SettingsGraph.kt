package com.sunyuanling.filesync.graph

import com.sunyuanling.filesync.ui.components.serverSetting.FileSettingsScreen
import com.sunyuanling.filesync.ui.components.serverSetting.LogSettingsScreen
import com.sunyuanling.filesync.ui.components.serverSetting.TransferSettingsScreen
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.sunyuanling.filesync.ui.components.serverSetting.ServerSettingsScreen
import com.sunyuanling.filesync.ui.components.serverSetting.SettingsScreen
import com.sunyuanling.filesync.ui.components.serverSetting.SyncSettingsScreen
import com.sunyuanling.filesync.ui.screen.AboutScreen
import com.sunyuanling.filesync.router.AboutDestination
import com.sunyuanling.filesync.router.FileSettingsDestination
import com.sunyuanling.filesync.router.LogSettingsDestination
import com.sunyuanling.filesync.router.ServerSettingsDestination
import com.sunyuanling.filesync.router.SettingsDestination
import com.sunyuanling.filesync.router.SyncSettingsDestination
import com.sunyuanling.filesync.router.TransferSettingsDestination


fun NavGraphBuilder.settingsGraph(navController: NavHostController) {
    composable<SettingsDestination> {
        SettingsScreen(navController = navController)
    }
    composable<ServerSettingsDestination> {
        ServerSettingsScreen(navController = navController)
    }
    composable<TransferSettingsDestination> {
        TransferSettingsScreen(navController = navController)
    }
    composable<SyncSettingsDestination> {
        SyncSettingsScreen(navController = navController)
    }
    composable<LogSettingsDestination> {
        LogSettingsScreen(navController = navController)
    }
    composable<FileSettingsDestination> {
        FileSettingsScreen(navController = navController)
    }
    composable<AboutDestination> {
        AboutScreen(navController = navController)
    }
}