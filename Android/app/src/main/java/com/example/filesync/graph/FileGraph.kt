package com.example.filesync.graph

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.example.filesync.router.*
import com.example.filesync.ui.screen.FileDetailScreen
import com.example.filesync.ui.screen.FileSearchScreen
import com.example.filesync.ui.screen.files.*

@RequiresApi(Build.VERSION_CODES.O)
fun NavGraphBuilder.fileGraph(navController: NavHostController) {
    composable<TransferDestination> {
        FileTransferListScreen(
            onBackClick = { navController.navigateUp() },
            navController = navController
        )
    }
    composable<FileDetailDestination> {
        val dest: FileDetailDestination = it.toRoute()
        FileDetailScreen(
            fileId = dest.fileId,
            onBackClick = { navController.navigateUp() }
        )
    }
    composable<FileUploadDestination> {
        FileUploadScreen(
            onBackClick = { navController.navigateUp() },
            navController = navController
        )
    }
    composable<FileSearchDestination> {
        FileSearchScreen(
            onBackClick = { navController.navigateUp() },
            onFileClick = { fileId ->
                navController.navigate(FileDetailDestination(fileId))
            }
        )
    }
}