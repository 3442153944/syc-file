// ui/screen/files/SyncListScreen.kt
// 职责：同步列表页——两个 tab：
//   「同步记录」：/sync/tasks 全量任务记录（状态/类型/文件/设备/时间/错误）
//   「待处理」  ：冲突（可解决/删除）+ 本设备待执行任务
package com.sunyuanling.filesync.ui.screen.files

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SuggestionChip
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import kotlinx.coroutines.launch
import com.sunyuanling.filesync.api.sync.SyncConflictInfo
import com.sunyuanling.filesync.api.sync.SyncTaskInfo
import com.sunyuanling.filesync.ui.components.files.ErrorCard
import com.sunyuanling.filesync.ui.viewModel.monitor.DeviceMonitorViewModel
import com.sunyuanling.filesync.ui.viewModel.sync.SyncListViewModel
import com.sunyuanling.filesync.util.TimeUtils
import com.sunyuanling.filesync.util.formatFileSize

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SyncListScreen(navController: NavController) {
    val vm = viewModel<SyncListViewModel>()
    val state by vm.uiState.collectAsState()
    var tabIndex by remember { mutableIntStateOf(0) }
    // 待确认的批量处理方式：accept_server / keep_local
    var confirmResolution by remember { mutableStateOf<String?>(null) }
    val listState = rememberLazyListState()
    val coroutineScope = rememberCoroutineScope()

    // 滚动到底部自动加载更多
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull() ?: return@derivedStateOf false
            last.index >= listState.layoutInfo.totalItemsCount - 2
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore && tabIndex == 0) vm.loadMore()
    }

    LaunchedEffect(Unit) { vm.refresh() }

    // 批量处理确认框（两种取舍本质都是"以服务器为准"，差别在本地更改的去向）
    confirmResolution?.let { resolution ->
        val acceptServer = resolution == "accept_server"
        AlertDialog(
            onDismissRequest = { confirmResolution = null },
            title = { Text(if (acceptServer) "强制与服务器对齐" else "保留本地更改") },
            text = {
                Text(
                    if (acceptServer)
                        "将放弃全部 ${state.conflicts.size} 条冲突的本地更改，主目录收敛为服务器版本（本地分叉副本从 .syncpending 丢弃）。"
                    else
                        "本地更改已暂存在 .syncpending，将以服务器当前版本为基准重新上报，成为新版本并同步到其它设备。共 ${state.conflicts.size} 条。"
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    vm.resolveAllConflicts(resolution)
                    confirmResolution = null
                }) { Text("确认") }
            },
            dismissButton = {
                TextButton(onClick = { confirmResolution = null }) { Text("取消") }
            }
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("同步列表") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    IconButton(onClick = { vm.refresh() }, enabled = !state.loading) {
                        Icon(Icons.Default.Refresh, contentDescription = "刷新")
                    }
                }
            )
        },
        floatingActionButton = {
            if (tabIndex == 0 && state.records.size >= 10) {
                FloatingActionButton(
                    onClick = {
                        coroutineScope.launch {
                            listState.animateScrollToItem(0)
                        }
                    },
                    containerColor = MaterialTheme.colorScheme.secondaryContainer
                ) {
                    Icon(Icons.Default.KeyboardArrowUp, contentDescription = "回到顶部")
                }
            }
        }
    ) { innerPadding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
        ) {
            val pendingCount = state.pendingTasks.size + state.conflicts.size
            TabRow(selectedTabIndex = tabIndex) {
                Tab(
                    selected = tabIndex == 0,
                    onClick = { tabIndex = 0 },
                    text = { Text("同步记录") }
                )
                Tab(
                    selected = tabIndex == 1,
                    onClick = { tabIndex = 1 },
                    text = { Text(if (pendingCount > 0) "待处理 ($pendingCount)" else "待处理") }
                )
            }

            if (state.loading) {
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(24.dp),
                    contentAlignment = Alignment.Center
                ) { CircularProgressIndicator() }
            }

            LazyColumn(
                state = listState,
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 16.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                item { Box(modifier = Modifier.padding(top = 4.dp)) }

                state.error?.let { item { ErrorCard(message = it) } }

                if (tabIndex == 0) {
                    if (!state.loading && state.records.isEmpty()) {
                        item { EmptyHint("暂无同步记录") }
                    }
                    items(state.records, key = { "r-${it.id}" }) { task ->
                        SyncTaskCard(task)
                    }
                } else {
                    // ---- 批量处理动作 ----
                    item {
                        Card(modifier = Modifier.fillMaxWidth()) {
                            Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                Text("批量处理", fontSize = 14.sp, fontWeight = FontWeight.Medium)
                                Text(
                                    text = "两种方式最终都以服务器为准，区别在本地更改的去向",
                                    fontSize = 12.sp,
                                    color = MaterialTheme.colorScheme.outline
                                )
                                Row(
                                    modifier = Modifier.fillMaxWidth(),
                                    horizontalArrangement = Arrangement.End
                                ) {
                                    TextButton(
                                        onClick = { vm.realign() },
                                    ) { Text("重新对齐", fontSize = 13.sp) }
                                    TextButton(
                                        onClick = { confirmResolution = "accept_server" },
                                        enabled = state.conflicts.isNotEmpty()
                                    ) { Text("全部用服务器版", fontSize = 13.sp) }
                                    TextButton(
                                        onClick = { confirmResolution = "keep_local" },
                                        enabled = state.conflicts.isNotEmpty()
                                    ) { Text("全部保留本地", fontSize = 13.sp) }
                                }
                            }
                        }
                    }
                    // ---- 冲突 ----
                    if (state.conflicts.isNotEmpty()) {
                        item { SectionTitle("冲突（保留两者，需人工取舍）") }
                        items(state.conflicts, key = { "c-${it.id}" }) { conflict ->
                            ConflictCard(
                                conflict = conflict,
                                busy = conflict.id in state.busyConflictIds,
                                onAcceptServer = { vm.resolveConflict(conflict.id, "accept_server") },
                                onKeepLocal = { vm.resolveConflict(conflict.id, "keep_local") },
                                onDelete = { vm.deleteConflict(conflict.id) }
                            )
                        }
                    }
                    // ---- 本设备待执行任务 ----
                    if (state.pendingTasks.isNotEmpty()) {
                        item { SectionTitle("本设备待执行任务") }
                        items(state.pendingTasks, key = { "p-${it.id}" }) { task ->
                            SyncTaskCard(task)
                        }
                    }
                    if (!state.loading && state.conflicts.isEmpty() && state.pendingTasks.isEmpty()) {
                        item { EmptyHint("没有待处理事项") }
                    }
                }

                item { Box(modifier = Modifier.padding(bottom = 8.dp)) }
                // 加载更多指示器
                if (state.loadingMore) {
                    item {
                        Box(modifier = Modifier.fillMaxWidth().padding(12.dp), contentAlignment = Alignment.Center) {
                            CircularProgressIndicator(modifier = Modifier.size(24.dp), strokeWidth = 2.dp)
                        }
                    }
                }
            }

        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        fontSize = 14.sp,
        fontWeight = FontWeight.Medium,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 8.dp, bottom = 2.dp)
    )
}

@Composable
private fun EmptyHint(text: String) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 32.dp),
        contentAlignment = Alignment.Center
    ) {
        Text(text, color = MaterialTheme.colorScheme.outline, fontSize = 14.sp)
    }
}

/** 单条同步任务卡片（记录 / 待执行共用）。 */
@Composable
private fun SyncTaskCard(task: SyncTaskInfo) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = task.fileName.ifEmpty { task.relativePath },
                    fontWeight = FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                StatusChip(task.syncStatus)
            }
            if (task.relativePath.isNotEmpty() && task.relativePath != task.fileName) {
                Text(
                    text = task.relativePath,
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.outline,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Text(
                    text = buildString {
                        append(SyncListViewModel.typeLabel(task.taskType))
                        if (task.fileSize > 0) append(" · ${formatFileSize(task.fileSize)}")
                        if (task.syncStatus == "syncing" && task.progress in 1..99) {
                            append(" · ${task.progress}%")
                        }
                        if (task.retryCount > 0) append(" · 重试${task.retryCount}")
                    },
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.outline
                )
                Text(
                    text = formatGoTime(task.createdAt),
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.outline
                )
            }
            task.errorMessage?.takeIf { it.isNotEmpty() }?.let {
                Text(
                    text = it,
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.error,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
    }
}

/** 冲突卡片：展示双方版本摘要，提供三种处理动作。 */
@Composable
private fun ConflictCard(
    conflict: SyncConflictInfo,
    busy: Boolean,
    onAcceptServer: () -> Unit,
    onKeepLocal: () -> Unit,
    onDelete: () -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = conflict.fileName.ifEmpty { conflict.relativePath },
                    fontWeight = FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Text(
                    text = formatGoTime(conflict.createdAt),
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.outline
                )
            }
            if (conflict.relativePath.isNotEmpty() && conflict.relativePath != conflict.fileName) {
                Text(
                    text = conflict.relativePath,
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.outline,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
            Text(
                text = "服务器 ${shortHash(conflict.serverHash)} · 本地 ${shortHash(conflict.localHash)}" +
                        " · 设备 ${conflict.deviceId.take(8)}",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.outline
            )
            HorizontalDivider()
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End
            ) {
                TextButton(onClick = onAcceptServer, enabled = !busy) { Text("用服务器版", fontSize = 13.sp) }
                TextButton(onClick = onKeepLocal, enabled = !busy) { Text("保留本地版", fontSize = 13.sp) }
                TextButton(onClick = onDelete, enabled = !busy) {
                    Text("删除记录", fontSize = 13.sp, color = MaterialTheme.colorScheme.error)
                }
            }
        }
    }
}

@Composable
private fun StatusChip(status: String) {
    SuggestionChip(
        onClick = {},
        label = { Text(SyncListViewModel.statusLabel(status), fontSize = 12.sp) }
    )
}

private fun shortHash(hash: String?): String =
    hash?.takeIf { it.isNotEmpty() }?.take(8) ?: "—"

private fun formatGoTime(iso: String): String {
    val millis = DeviceMonitorViewModel.parseIsoToMillis(iso)
    return if (millis > 0) TimeUtils.format(millis, "MM-dd HH:mm") else ""
}
