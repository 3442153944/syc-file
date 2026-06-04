package com.sunyuanling.filesync.graph

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.sunyuanling.filesync.ui.screen.FileDetailScreen
import com.sunyuanling.filesync.ui.screen.FileSearchScreen
import com.sunyuanling.filesync.router.FileDetailDestination
import com.sunyuanling.filesync.router.FileSearchDestination
import com.sunyuanling.filesync.router.FileUploadDestination
import com.sunyuanling.filesync.router.TransferDestination
import com.sunyuanling.filesync.ui.screen.files.FileTransferListScreen
import com.sunyuanling.filesync.ui.screen.files.FileUploadScreen

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