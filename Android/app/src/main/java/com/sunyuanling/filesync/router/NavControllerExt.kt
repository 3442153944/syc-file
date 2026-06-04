package com.sunyuanling.filesync.router

import androidx.navigation.NavController
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavOptionsBuilder

fun NavController.navigateToMainTab(destination: Any) {
    navigate(destination) {
        popUpTo(graph.findStartDestination().id) {
            saveState = true
        }
        launchSingleTop = true
        restoreState = true
    }
}

fun NavController.navigateToDetail(destination: Any) {
    navigate(destination) {
        launchSingleTop = true
    }
}

fun NavController.navigateToDetail(
    destination: Any,
    builder: NavOptionsBuilder.() -> Unit
) {
    navigate(destination) {
        launchSingleTop = true
        builder()
    }
}

fun NavController.navigateAndReplace(destination: Any) {
    navigate(destination) {
        popUpTo(currentBackStackEntry?.destination?.route ?: return@navigate) {
            inclusive = true
        }
    }
}

fun NavController.navigateAndClearBackStack(destination: Any) {
    navigate(destination) {
        popUpTo(0) { inclusive = true }
    }
}

fun NavController.safeNavigateUp(): Boolean {
    return if (previousBackStackEntry != null) {
        navigateUp()
    } else {
        navigate(HomeDestination) {
            popUpTo(graph.findStartDestination().id) {
                saveState = true
            }
            launchSingleTop = true
            restoreState = true
        }
        true
    }
}
