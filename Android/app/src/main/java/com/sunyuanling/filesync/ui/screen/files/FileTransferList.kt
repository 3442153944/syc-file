package com.sunyuanling.filesync.ui.screen.files

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.annotation.RequiresApi
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Sort
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.sunyuanling.filesync.ui.viewModel.transmission.FileTransferStatus
import com.sunyuanling.filesync.ui.viewModel.files.FileTransferListViewModel
import com.sunyuanling.filesync.ui.viewModel.files.SortBy


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
    val successMessage by viewModel.successMessage.collectAsState()
    val total by viewModel.total.collectAsState()
    val isMultiSelect by viewModel.isMultiSelect.collectAsState()
    val selectedItems by viewModel.selectedItems.collectAsState()

    var showFilterMenu by remember { mutableStateOf(false) }
    var showSortMenu by remember { mutableStateOf(false) }

    val filteredItems = remember(transferItems, filterStatus, sortBy) {
        viewModel.getFilteredAndSortedItems()
    }

    val listState = rememberLazyListState()

    val reachedBottom by remember {
        derivedStateOf {
            val lastVisibleItem = listState.layoutInfo.visibleItemsInfo.lastOrNull()
            lastVisibleItem != null && lastVisibleItem.index >= listState.layoutInfo.totalItemsCount - 3
        }
    }
    LaunchedEffect(reachedBottom) {
        if (reachedBottom && !isLoading) viewModel.loadMore()
    }

    val snackbarHostState = remember { SnackbarHostState() }
    LaunchedEffect(errorMessage) {
        errorMessage?.let { snackbarHostState.showSnackbar(it); viewModel.dismissError() }
    }
    LaunchedEffect(successMessage) {
        successMessage?.let { snackbarHostState.showSnackbar(it); viewModel.dismissSuccess() }
    }

    // 物理返回键：多选模式下退出多选而不是返回页面
    BackHandler(enabled = isMultiSelect) {
        viewModel.exitMultiSelect()
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbarHostState) },
        topBar = {
            if (isMultiSelect) {
                // 多选模式 TopBar
                TopAppBar(
                    title = { Text("已选 ${selectedItems.size} 项") },
                    navigationIcon = {
                        IconButton(onClick = { viewModel.exitMultiSelect() }) {
                            Icon(Icons.Default.Close, "退出多选")
                        }
                    },
                    actions = {
                        TextButton(onClick = { viewModel.selectAll() }) {
                            Text("全选")
                        }
                        IconButton(
                            onClick = { viewModel.deleteSelected() },
                            enabled = selectedItems.isNotEmpty()
                        ) {
                            Icon(Icons.Default.Delete, "删除选中")
                        }
                    },
                    colors = TopAppBarDefaults.topAppBarColors(
                        containerColor = MaterialTheme.colorScheme.primaryContainer
                    )
                )
            } else {
                // 普通模式 TopBar（原来的不变）
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
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, "返回")
                        }
                    },
                    actions = {
                        // 筛选、排序、更多按钮（原来的不变）
                        Box {
                            IconButton(onClick = { showFilterMenu = true }) {
                                Icon(Icons.Default.FilterList, "筛选")
                            }
                            DropdownMenu(expanded = showFilterMenu, onDismissRequest = { showFilterMenu = false }) {
                                DropdownMenuItem(
                                    text = { Text("全部") },
                                    onClick = { viewModel.setFilter(null); showFilterMenu = false },
                                    leadingIcon = { if (filterStatus == null) Icon(Icons.Default.Check, null) }
                                )
                                FileTransferStatus.entries.forEach { status ->
                                    DropdownMenuItem(
                                        text = { Text(status.displayName) },
                                        onClick = { viewModel.setFilter(status); showFilterMenu = false },
                                        leadingIcon = { if (filterStatus == status) Icon(Icons.Default.Check, null) }
                                    )
                                }
                            }
                        }
                        Box {
                            IconButton(onClick = { showSortMenu = true }) {
                                Icon(Icons.AutoMirrored.Filled.Sort, "排序")
                            }
                            DropdownMenu(expanded = showSortMenu, onDismissRequest = { showSortMenu = false }) {
                                SortBy.entries.forEach { sort ->
                                    DropdownMenuItem(
                                        text = { Text(sort.displayName) },
                                        onClick = { viewModel.setSortBy(sort); showSortMenu = false },
                                        leadingIcon = { if (sortBy == sort) Icon(Icons.Default.Check, null) }
                                    )
                                }
                            }
                        }
                        Box {
                            var showClearMenu by remember { mutableStateOf(false) }
                            IconButton(onClick = { showClearMenu = true }) {
                                Icon(Icons.Default.MoreVert, "更多")
                            }
                            DropdownMenu(expanded = showClearMenu, onDismissRequest = { showClearMenu = false }) {
                                DropdownMenuItem(
                                    text = { Text("清空已完成") },
                                    onClick = { viewModel.clearCompleted(); showClearMenu = false }
                                )
                                DropdownMenuItem(
                                    text = { Text("清空全部") },
                                    onClick = { viewModel.clearAll(); showClearMenu = false }
                                )
                            }
                        }
                    }
                )
            }
        }
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = isLoading && transferItems.isEmpty(),
            onRefresh = { viewModel.loadTransferHistory() },
            modifier = modifier.fillMaxSize().padding(padding)
        ) {
            if (filteredItems.isEmpty() && !isLoading) {
                Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Column(
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Icon(Icons.Default.CloudQueue, null, Modifier.size(64.dp), tint = MaterialTheme.colorScheme.outline)
                        Text("暂无传输记录", color = MaterialTheme.colorScheme.outline)
                        TextButton(onClick = { viewModel.loadTransferHistory() }) { Text("点击刷新") }
                    }
                }
            } else {
                LazyColumn(
                    state = listState,
                    contentPadding = PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    items(filteredItems, key = { it.id }) { item ->
                        val isSelected = selectedItems.any { it.id == item.id }
                        TransferItemCard(
                            item = item,
                            isMultiSelect = isMultiSelect,
                            isSelected = isSelected,
                            onLongClick = { viewModel.enterMultiSelect(item) },
                            onClick = {
                                if (isMultiSelect) viewModel.toggleSelectItem(item)
                            },
                            onRetry = { viewModel.retryTransfer(item.id) },
                            onCancel = { viewModel.cancelTransfer(item.id) },
                            onPause = { viewModel.pauseTransfer(item.id) },
                            onResume = { viewModel.resumeTransfer(item.id) },
                            onRemove = { viewModel.deleteDownloadHistory(listOf(item.id)) }
                        )
                    }
                    if (isLoading && transferItems.isNotEmpty()) {
                        item {
                            Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
                                CircularProgressIndicator(modifier = Modifier.size(24.dp))
                            }
                        }
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




