package com.sunyuanling.filesync.router

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import com.sunyuanling.filesync.graph.authGraph
import com.sunyuanling.filesync.graph.fileGraph
import com.sunyuanling.filesync.graph.mainGraph
import com.sunyuanling.filesync.graph.settingsGraph

@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@Composable
fun AppNavHost(
    navController: NavHostController,
    modifier: Modifier = Modifier
) {
    NavHost(
        navController = navController,
        startDestination = PermissionDestination,
        modifier = modifier
    ) {
        mainGraph(navController)
        fileGraph(navController)
        settingsGraph(navController)
        authGraph(navController)
    }
}