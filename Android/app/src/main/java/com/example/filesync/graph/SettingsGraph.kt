package com.example.filesync.graph

import com.example.filesync.ui.components.serverSetting.FileSettingsScreen
import com.example.filesync.ui.components.serverSetting.LogSettingsScreen
import com.example.filesync.ui.components.serverSetting.TransferSettingsScreen
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.example.filesync.router.*
import com.example.filesync.ui.components.serverSetting.ServerSettingsScreen
import com.example.filesync.ui.components.serverSetting.SettingsScreen
import com.example.filesync.ui.components.serverSetting.SyncSettingsScreen
import com.example.filesync.ui.screen.AboutScreen


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