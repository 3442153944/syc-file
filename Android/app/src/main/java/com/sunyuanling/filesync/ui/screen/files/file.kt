// ui/screen/files/file.kt
package com.sunyuanling.filesync.ui.screen.files

import android.os.Build
import android.util.Log
import androidx.activity.compose.BackHandler
import androidx.annotation.RequiresApi
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.FolderCopy
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.router.FileUploadDestination
import com.sunyuanling.filesync.router.SyncFolderMapDestination
import com.sunyuanling.filesync.router.SyncListDestination
import com.sunyuanling.filesync.router.SyncSettingsDestination
import com.sunyuanling.filesync.router.navigateToDetail
import com.sunyuanling.filesync.sync.SyncEngine
import com.sunyuanling.filesync.ui.components.files.DirectoryPickerScreen
import com.sunyuanling.filesync.ui.components.files.ErrorCard
import com.sunyuanling.filesync.ui.components.files.FileItemCard
import com.sunyuanling.filesync.ui.components.files.FileListHeader
import com.sunyuanling.filesync.ui.components.files.FileListStats
import com.sunyuanling.filesync.ui.components.files.LoadingIndicator
import com.sunyuanling.filesync.ui.components.files.diskItems
import com.sunyuanling.filesync.ui.viewModel.files.ActiveDiskViewModel
import com.sunyuanling.filesync.ui.viewModel.files.FileListViewModel
import com.sunyuanling.filesync.ui.viewModel.transmission.DownloadListViewModel
import com.sunyuanling.filesync.util.RootHelper
import com.sunyuanling.filesync.api.file.FileItem
import com.sunyuanling.filesync.router.TransferDestination
import com.sunyuanling.filesync.util.formatFileSize
import java.io.File

@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun FileScreen(
    modifier: Modifier = Modifier,
    navController: NavController
) {
    val context = LocalContext.current
    val diskViewModel = viewModel<ActiveDiskViewModel>()
    val fileListViewModel = viewModel<FileListViewModel>()
    val downloadViewModel = viewModel<DownloadListViewModel>()

    val diskData by diskViewModel.diskData.collectAsState()
    val diskLoading by diskViewModel.loading.collectAsState()
    val diskError by diskViewModel.error.collectAsState()

    val fileData by fileListViewModel.fileData.collectAsState()
    val fileLoading by fileListViewModel.loading.collectAsState()
    val fileError by fileListViewModel.error.collectAsState()
    val pathStack by fileListViewModel.pathStack.collectAsState()

    var currentDiskPath by remember { mutableStateOf<String?>(null) }
    var showDownloadDialog by remember { mutableStateOf(false) }
    var selectedFileForDownload by remember { mutableStateOf<FileItem?>(null) }
    var showDirectoryPicker by remember { mutableStateOf(false) }

    // 判断是否在磁盘根目录
    val isAtDiskRoot = remember(fileData, currentDiskPath) {
        val parentPath = fileData?.parentPath
        parentPath.isNullOrEmpty() || parentPath == currentDiskPath
    }

    // 系统返回键处理
    BackHandler(enabled = currentDiskPath != null && !showDirectoryPicker) {
        try {
            when {
                pathStack.isNotEmpty() -> {
                    fileListViewModel.navigateBack()
                }
                !isAtDiskRoot && fileData?.parentPath?.isNotEmpty() == true -> {
                    fileListViewModel.navigateToParent()
                }
                else -> {
                    currentDiskPath = null
                    fileListViewModel.clearState()
                }
            }
        }
        catch (e: Exception) {
            e.printStackTrace()
            Log.e("FileScreen", "BackHandler error: ${e.message}")
        }

    }

    // 检测 Root 状态
    var isRooted by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        isRooted = RootHelper.isDeviceRooted()
    }

    // 目录选择器页面
    if (showDirectoryPicker && selectedFileForDownload != null) {
        DirectoryPickerScreen(
            isRooted = isRooted,
            onDirectorySelected = { selectedPath ->
                val item = selectedFileForDownload!!
                val parentPath = item.path.substringBeforeLast(File.separator)

                downloadViewModel.addDownload(
                    path = item.path,
                    name = item.name,
                    saveDir = File(selectedPath),
                    deviceId = null
                )

                showDirectoryPicker = false
                selectedFileForDownload = null
                Log.d("FileScreen", "保存目录: $selectedPath")
                //跳转到传输页
                navController.navigate(TransferDestination)
            },
            onDismiss = {
                showDirectoryPicker = false
                selectedFileForDownload = null
            },
            fileItem = selectedFileForDownload!!
        )
        return // 显示目录选择器时，不显示文件列表
    }

    // 下载确认对话框（简化版，只显示文件信息）
    if (showDownloadDialog && selectedFileForDownload != null) {
        AlertDialog(
            onDismissRequest = {
                showDownloadDialog = false
                selectedFileForDownload = null
            },
            title = {
                Text(if (selectedFileForDownload!!.isDir) "下载文件夹" else "下载文件")
            },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("文件名: ${selectedFileForDownload!!.name}")
                    if (!selectedFileForDownload!!.isDir) {
                        Text("大小: ${formatFileSize(selectedFileForDownload!!.size)}")
                    }
                    Text("准备选择保存位置...")
                }
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        showDownloadDialog = false
                        showDirectoryPicker = true
                    }
                ) {
                    Text("选择保存位置")
                }
            },
            dismissButton = {
                TextButton(
                    onClick = {
                        showDownloadDialog = false
                        selectedFileForDownload = null
                    }
                ) {
                    Text("取消")
                }
            }
        )
    }

    if (currentDiskPath != null) {
        LaunchedEffect(currentDiskPath) {
            fileListViewModel.loadDirectory(currentDiskPath!!)
        }

        LazyColumn(
            modifier = modifier
                .fillMaxSize()
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            item {
                FileListHeader(
                    currentPath = fileData?.currentPath ?: currentDiskPath!!,
                    canGoBack = true,
                    loading = fileLoading,
                    onBack = {
                        when {
                            pathStack.isNotEmpty() -> {
                                fileListViewModel.navigateBack()
                            }

                            !isAtDiskRoot && fileData?.parentPath?.isNotEmpty() == true -> {
                                fileListViewModel.navigateToParent()
                            }

                            else -> {
                                currentDiskPath = null
                                fileListViewModel.clearState()
                            }
                        }
                    },
                    onRefresh = { fileListViewModel.refresh() }
                )
            }

            fileData?.let { data ->
                item {
                    FileListStats(
                        totalCount = data.totalCount,
                        dirCount = data.dirCount,
                        fileCount = data.fileCount
                    )
                }
            }

            if (fileLoading) {
                item { LoadingIndicator() }
            }

            if (fileError != null) {
                item { ErrorCard(message = fileError!!) }
            }

            val fileItems = fileData?.items ?: emptyList()
            val sortedItems = fileItems.sortedWith(compareBy({ !it.isDir }, { it.name.lowercase() }))

            items(sortedItems, key = { it.path }) { item ->
                FileItemCard(
                    item = item,
                    onClick = {
                        if (item.isDir) {
                            fileListViewModel.navigateTo(item.path)
                        } else {
                            selectedFileForDownload = item
                            showDownloadDialog = true
                        }
                    },
                    onLongClick = if (item.isDir) {
                        {
                            selectedFileForDownload = item
                            showDownloadDialog = true
                        }
                    } else null
                )
            }
        }
    } else {
        LazyColumn(
            modifier = modifier
                .fillMaxSize()
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            item {
                DiskListHeader(
                    title = "可用磁盘 (${diskData?.allowedCount ?: 0})",
                    loading = diskLoading,
                    onRefresh = { diskViewModel.loadDisks()},
                    navController = navController
                )
            }

            // 同步入口：设置（保活开关等）+ 同步列表（记录/待处理）
            item { SyncSectionCard(navController = navController) }

            if (diskLoading) {
                item { LoadingIndicator() }
            }

            if (diskError != null) {
                item { ErrorCard(message = diskError!!) }
            }
            diskData?.allowedCount?.let {
                if(it>0){
                    item{
                        Button(onClick = { navController.navigateToDetail(FileUploadDestination) }) {
                            Text("上传文件")
                        }
                    }
                }
            }

            diskItems(
                disks = diskData?.allowedDisks ?: emptyList(),
                onDiskClick = { disk ->
                    currentDiskPath = disk.path
                }
            )

            val disabledDisks = diskData?.allDisks?.filter { !it.isAllowed } ?: emptyList()
            if (disabledDisks.isNotEmpty()) {
                item {
                    Text(
                        text = "其他磁盘",
                        fontSize = 16.sp,
                        fontWeight = FontWeight.Medium,
                        modifier = Modifier.padding(top = 8.dp)
                    )
                }

                diskItems(
                    disks = disabledDisks,
                    onDiskClick = null
                )
            }
        }
    }
}

/**
 * 同步区块：文件页首屏入口卡片。
 * - 同步设置：强制保活开关、自动同步等（SyncSettingsScreen）
 * - 同步列表：同步记录 + 待处理事项（SyncListScreen）
 * 副标题实时显示保活开关与 WS 连接状态。
 */
@Composable
private fun SyncSectionCard(navController: NavController) {
    val wsState by WebSocketManager.connectionState.collectAsState()
    val engineRunning by SyncEngine.running.collectAsState()
    val engineActivity by SyncEngine.lastActivity.collectAsState()
    val statusText = buildString {
        append(if (AppConfig.forceKeepAliveEnabled) "保活已开启" else "保活未开启")
        append(" · ")
        append(
            when (wsState) {
                is WsState.Connected -> "同步连接在线"
                is WsState.Connecting -> "连接中…"
                is WsState.Disconnected -> "连接已断开"
                is WsState.Error -> "连接异常"
            }
        )
    }
    Card(modifier = Modifier.fillMaxWidth()) {
        Column {
            SyncEntryRow(
                icon = { Icon(Icons.Default.Settings, contentDescription = null) },
                title = "同步设置",
                subtitle = statusText,
                onClick = { navController.navigate(SyncSettingsDestination) }
            )
            HorizontalDivider(modifier = Modifier.padding(horizontal = 16.dp))
            SyncEntryRow(
                icon = { Icon(Icons.Default.FolderCopy, contentDescription = null) },
                title = "同步文件夹",
                subtitle = "本设备的本地目录映射",
                onClick = { navController.navigate(SyncFolderMapDestination) }
            )
            HorizontalDivider(modifier = Modifier.padding(horizontal = 16.dp))
            SyncEntryRow(
                icon = { Icon(Icons.Default.Sync, contentDescription = null) },
                title = "同步列表",
                subtitle = if (engineRunning) "引擎运行中 · $engineActivity" else "同步记录与待处理事项",
                onClick = { navController.navigate(SyncListDestination) }
            )
        }
    }
}

@Composable
private fun SyncEntryRow(
    icon: @Composable () -> Unit,
    title: String,
    subtitle: String,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        icon()
        Column(modifier = Modifier.weight(1f)) {
            Text(text = title, fontSize = 16.sp, fontWeight = FontWeight.Medium)
            Text(
                text = subtitle,
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.outline
            )
        }
        Icon(
            Icons.AutoMirrored.Filled.KeyboardArrowRight,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.outline
        )
    }
}

