// ui/screen/files/FileTransferListScreen.kt
package com.sunyuanling.filesync.ui.screen.files

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.sunyuanling.filesync.ui.viewModel.transmission.FileTransferStatus
import com.sunyuanling.filesync.ui.viewModel.files.FileTransferItem
import com.sunyuanling.filesync.ui.viewModel.files.FileTransferListViewModel
import com.sunyuanling.filesync.ui.viewModel.files.SortBy
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

@RequiresApi(Build.VERSION_CODES.O)
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileTransferListScreen(
    onBackClick: () -> Unit,
    modifier: Modifier = Modifier,
    navController: NavController,
    viewModel: FileTransferListViewModel = viewModel()
) {
    val transferItems by viewModel.transferItems.collectAsState()
    val filterStatus by viewModel.filterStatus.collectAsState()
    val sortBy by viewModel.sortBy.collectAsState()
    val isLoading by viewModel.isLoading.collectAsState()
    val errorMessage by viewModel.errorMessage.collectAsState()
    val total by viewModel.total.collectAsState()

    var showFilterMenu by remember { mutableStateOf(false) }
    var showSortMenu by remember { mutableStateOf(false) }

    val filteredItems = remember(transferItems, filterStatus, sortBy) {
        viewModel.getFilteredAndSortedItems()
    }

    val listState = rememberLazyListState()

    // 滚动到底部时加载更多
    val reachedBottom by remember {
        derivedStateOf {
            val lastVisibleItem = listState.layoutInfo.visibleItemsInfo.lastOrNull()
            lastVisibleItem != null && lastVisibleItem.index >= listState.layoutInfo.totalItemsCount - 3
        }
    }
    LaunchedEffect(reachedBottom) {
        if (reachedBottom && !isLoading) {
            viewModel.loadMore()
        }
    }

    // 错误提示 Snackbar
    val snackbarHostState = remember { SnackbarHostState() }
    LaunchedEffect(errorMessage) {
        errorMessage?.let {
            snackbarHostState.showSnackbar(it)
            viewModel.dismissError()
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbarHostState) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text("传输列表")
                        if (total > 0) {
                            Text(
                                "共 $total 条记录",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBackClick) {
                        Icon(Icons.Default.ArrowBack, "返回")
                    }
                },
                actions = {
                    // 筛选按钮
                    Box {
                        IconButton(onClick = { showFilterMenu = true }) {
                            Icon(Icons.Default.FilterList, "筛选")
                        }
                        DropdownMenu(
                            expanded = showFilterMenu,
                            onDismissRequest = { showFilterMenu = false }
                        ) {
                            DropdownMenuItem(
                                text = { Text("全部") },
                                onClick = {
                                    viewModel.setFilter(null)
                                    showFilterMenu = false
                                },
                                leadingIcon = {
                                    if (filterStatus == null) {
                                        Icon(Icons.Default.Check, null)
                                    }
                                }
                            )
                            FileTransferStatus.entries.forEach { status ->
                                DropdownMenuItem(
                                    text = { Text(status.displayName) },
                                    onClick = {
                                        viewModel.setFilter(status)
                                        showFilterMenu = false
                                    },
                                    leadingIcon = {
                                        if (filterStatus == status) {
                                            Icon(Icons.Default.Check, null)
                                        }
                                    }
                                )
                            }
                        }
                    }

                    // 排序按钮
                    Box {
                        IconButton(onClick = { showSortMenu = true }) {
                            Icon(Icons.Default.Sort, "排序")
                        }
                        DropdownMenu(
                            expanded = showSortMenu,
                            onDismissRequest = { showSortMenu = false }
                        ) {
                            SortBy.entries.forEach { sort ->
                                DropdownMenuItem(
                                    text = { Text(sort.displayName) },
                                    onClick = {
                                        viewModel.setSortBy(sort)
                                        showSortMenu = false
                                    },
                                    leadingIcon = {
                                        if (sortBy == sort) {
                                            Icon(Icons.Default.Check, null)
                                        }
                                    }
                                )
                            }
                        }
                    }

                    // 更多操作
                    Box {
                        var showClearMenu by remember { mutableStateOf(false) }
                        IconButton(onClick = { showClearMenu = true }) {
                            Icon(Icons.Default.MoreVert, "更多")
                        }
                        DropdownMenu(
                            expanded = showClearMenu,
                            onDismissRequest = { showClearMenu = false }
                        ) {
                            DropdownMenuItem(
                                text = { Text("清空已完成") },
                                onClick = {
                                    viewModel.clearCompleted()
                                    showClearMenu = false
                                }
                            )
                            DropdownMenuItem(
                                text = { Text("清空全部") },
                                onClick = {
                                    viewModel.clearAll()
                                    showClearMenu = false
                                }
                            )
                        }
                    }
                }
            )
        }
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = isLoading && transferItems.isEmpty(),
            onRefresh = { viewModel.loadTransferHistory() },
            modifier = modifier
                .fillMaxSize()
                .padding(padding)
        ) {
            if (filteredItems.isEmpty() && !isLoading) {
                // 空状态
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    Column(
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Icon(
                            Icons.Default.CloudQueue,
                            contentDescription = null,
                            modifier = Modifier.size(64.dp),
                            tint = MaterialTheme.colorScheme.outline
                        )
                        Text(
                            "暂无传输记录",
                            color = MaterialTheme.colorScheme.outline
                        )
                        TextButton(onClick = { viewModel.loadTransferHistory() }) {
                            Text("点击刷新")
                        }
                    }
                }
            } else {
                LazyColumn(
                    state = listState,
                    contentPadding = PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    items(filteredItems, key = { it.id }) { item ->
                        TransferItemCard(
                            item = item,
                            onRetry = { viewModel.retryTransfer(item.id) },
                            onCancel = { viewModel.cancelTransfer(item.id) },
                            onPause = { viewModel.pauseTransfer(item.id) },
                            onResume = { viewModel.resumeTransfer(item.id) },
                            onRemove = { viewModel.removeTransferItem(item.id) }
                        )
                    }

                    // 底部加载指示器
                    if (isLoading && transferItems.isNotEmpty()) {
                        item {
                            Box(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(16.dp),
                                contentAlignment = Alignment.Center
                            ) {
                                CircularProgressIndicator(modifier = Modifier.size(24.dp))
                            }
                        }
                    }
                }
            }
        }
    }
}

@RequiresApi(Build.VERSION_CODES.O)
@Composable
fun TransferItemCard(
    item: FileTransferItem,
    onRetry: () -> Unit,
    onCancel: () -> Unit,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onRemove: () -> Unit
) {
    Card(
        modifier = Modifier.fillMaxWidth()
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            // 文件信息行
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Icon(
                    if (item.isDir) Icons.Default.Folder else Icons.AutoMirrored.Filled.InsertDriveFile,
                    contentDescription = null,
                    modifier = Modifier.size(40.dp),
                    tint = when (item.status) {
                        FileTransferStatus.COMPLETED -> MaterialTheme.colorScheme.tertiary
                        FileTransferStatus.FAILED -> MaterialTheme.colorScheme.error
                        else -> MaterialTheme.colorScheme.primary
                    }
                )
                Spacer(Modifier.width(12.dp))
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        item.name,
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.Medium,
                        maxLines = 1
                    )
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Text(
                            formatFileSize(item.size),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                        if (item.isDir && item.childrenCount > 0) {
                            Text(
                                "· ${item.childrenCount} 项",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                }

                TransferStatusChip(item.status)
            }

            // 时间信息
            if (item.startTime > 0) {
                Text(
                    formatDate("yyyy-MM-dd HH:mm:ss", item.startTime),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }

            // 进度条（传输中或暂停时显示）
            if (item.status.showProgress) {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween
                    ) {
                        Text(
                            "${(item.progress * 100).toInt()}%",
                            style = MaterialTheme.typography.labelSmall
                        )
                        if (item.status == FileTransferStatus.TRANSFERRING) {
                            Text(
                                formatSpeed(item.speed),
                                style = MaterialTheme.typography.labelSmall
                            )
                        } else {
                            Text(
                                "已暂停",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                    LinearProgressIndicator(
                        progress = { item.progress },
                        modifier = Modifier.fillMaxWidth()
                    )
                }
            }

            // 错误信息
            if (item.status == FileTransferStatus.FAILED && item.errorMessage != null) {
                Text(
                    item.errorMessage,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error
                )
            }

            // 操作按钮
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically
            ) {
                when {
                    item.status.canRetry -> {
                        TextButton(onClick = onRetry) {
                            Icon(Icons.Default.Refresh, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(4.dp))
                            Text("重试")
                        }
                    }
                    item.status == FileTransferStatus.PAUSED -> {
                        TextButton(onClick = onResume) {
                            Icon(Icons.Default.PlayArrow, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(4.dp))
                            Text("恢复")
                        }
                    }
                    item.status.canPause -> {
                        TextButton(onClick = onPause) {
                            Icon(Icons.Default.Pause, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(4.dp))
                            Text("暂停")
                        }
                    }
                }

                if (item.status.canCancel) {
                    TextButton(onClick = onCancel) {
                        Icon(Icons.Default.Cancel, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(4.dp))
                        Text("取消")
                    }
                }

                if (item.status.canDelete) {
                    IconButton(onClick = onRemove) {
                        Icon(Icons.Default.Delete, "删除记录")
                    }
                }
            }
        }
    }
}

@Composable
fun TransferStatusChip(status: FileTransferStatus) {
    val (backgroundColor, contentColor, icon) = when (status) {
        FileTransferStatus.WAITING -> Triple(
            MaterialTheme.colorScheme.secondaryContainer,
            MaterialTheme.colorScheme.onSecondaryContainer,
            Icons.Default.Schedule
        )
        FileTransferStatus.TRANSFERRING -> Triple(
            MaterialTheme.colorScheme.primaryContainer,
            MaterialTheme.colorScheme.onPrimaryContainer,
            Icons.Default.CloudUpload
        )
        FileTransferStatus.PAUSED -> Triple(
            MaterialTheme.colorScheme.surfaceVariant,
            MaterialTheme.colorScheme.onSurfaceVariant,
            Icons.Default.Pause
        )
        FileTransferStatus.COMPLETED -> Triple(
            MaterialTheme.colorScheme.tertiaryContainer,
            MaterialTheme.colorScheme.onTertiaryContainer,
            Icons.Default.CheckCircle
        )
        FileTransferStatus.FAILED -> Triple(
            MaterialTheme.colorScheme.errorContainer,
            MaterialTheme.colorScheme.onErrorContainer,
            Icons.Default.Error
        )
        FileTransferStatus.CANCELLED -> Triple(
            MaterialTheme.colorScheme.surfaceVariant,
            MaterialTheme.colorScheme.onSurfaceVariant,
            Icons.Default.Cancel
        )
    }

    Surface(
        shape = RoundedCornerShape(12.dp),
        color = backgroundColor
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(4.dp)
        ) {
            Icon(
                icon,
                contentDescription = null,
                modifier = Modifier.size(16.dp),
                tint = contentColor
            )
            Text(
                status.displayName,
                style = MaterialTheme.typography.labelSmall,
                color = contentColor
            )
        }
    }
}

/**
 * 格式化时间戳为可读时间
 */
@RequiresApi(Build.VERSION_CODES.O)
private fun formatDate(pattern: String, millis: Long): String {
    if (millis <= 0) return ""
    return try {
        val instant = Instant.ofEpochMilli(millis)
        val zdt = instant.atZone(ZoneId.systemDefault())
        zdt.format(DateTimeFormatter.ofPattern(pattern))
    } catch (e: Exception) {
        ""
    }
}

fun formatFileSize(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var value = bytes.toDouble()
    var unitIndex = -1
    while (value >= 1024 && unitIndex < units.size - 1) {
        value /= 1024
        unitIndex++
    }
    return "%.2f %s".format(value, units[unitIndex])
}

fun formatSpeed(bytesPerSecond: Long): String {
    return when {
        bytesPerSecond < 1024 -> "$bytesPerSecond B/s"
        bytesPerSecond < 1024 * 1024 -> "${bytesPerSecond / 1024} KB/s"
        else -> "%.1f MB/s".format(bytesPerSecond / (1024.0 * 1024.0))
    }
}