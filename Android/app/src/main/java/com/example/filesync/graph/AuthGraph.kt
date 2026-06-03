package com.example.filesync.graph

import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import com.example.filesync.router.*
import com.example.filesync.ui.screen.permission.PermissionScreen
import com.example.filesync.ui.screen.person.LoginScreen

fun NavGraphBuilder.authGraph(navController: NavHostController) {
    composable<PermissionDestination> {
        PermissionScreen(
            onPermissionsGranted = {
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