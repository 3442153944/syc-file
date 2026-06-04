package com.sunyuanling.filesync.graph

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.sunyuanling.filesync.ui.screen.files.FileScreen
import com.sunyuanling.filesync.ui.screen.person.PersonalScreen
import com.sunyuanling.filesync.router.FilesDestination
import com.sunyuanling.filesync.router.HomeDestination
import com.sunyuanling.filesync.router.MonitorDestination
import com.sunyuanling.filesync.router.PersonalDestination
import com.sunyuanling.filesync.ui.screen.HomeScreen
import com.sunyuanling.filesync.ui.screen.MonitorScreen

@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
fun NavGraphBuilder.mainGraph(navController: NavHostController) {
    composable<HomeDestination> {
        HomeScreen(navController = navController)
    }
    composable<FilesDestination> {
        FileScreen(navController = navController)
    }
    composable<MonitorDestination> {
        MonitorScreen(navController = navController)
    }
    composable<PersonalDestination> {
        PersonalScreen(navController = navController)
    }
}