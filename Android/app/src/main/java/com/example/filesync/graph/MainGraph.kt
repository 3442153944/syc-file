package com.example.filesync.graph

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.example.filesync.router.*
import com.example.filesync.ui.screen.*
import com.example.filesync.ui.screen.files.FileScreen
import com.example.filesync.ui.screen.person.PersonalScreen

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