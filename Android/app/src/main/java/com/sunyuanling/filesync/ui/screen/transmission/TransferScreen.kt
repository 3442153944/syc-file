// ui/screen/transmission/TransferScreen.kt
package com.sunyuanling.filesync.ui.screen.transmission

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.sunyuanling.filesync.dataClass.DownloadItem
import com.sunyuanling.filesync.dataClass.DownloadStatus
import com.sunyuanling.filesync.ui.viewModel.transmission.DownloadListViewModel
import com.sunyuanling.filesync.util.formatFileSize
import java.io.File

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TransferScreen(
    onBackClick: () -> Unit,
    modifier: Modifier = Modifier
) {
    val downloadViewModel = viewModel<DownloadListViewModel>()
    val downloadList by remember { derivedStateOf { downloadViewModel.downloadList } }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("传输列表 (${downloadList.size})") },
                navigationIcon = {
                    IconButton(onClick = onBackClick) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                }
            )
        }
    ) { paddingValues ->
        if (downloadList.isEmpty()) {
            Box(
                modifier = modifier
                    .fillMaxSize()
                    .padding(paddingValues),
                contentAlignment = Alignment.Center
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Icon(
                        imageVector = Icons.Default.Download,
                        contentDescription = null,
                        modifier = Modifier.size(64.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Spacer(modifier = Modifier.height(16.dp))
                    Text(
                        "暂无下载任务",
                        style = MaterialTheme.typography.titleMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
        } else {
            LazyColumn(
                modifier = modifier
                    .fillMaxSize()
                    .padding(paddingValues)
                    .padding(16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                items(downloadList, key = { it.downloadId }) { item ->
                    DownloadItemCard(
                        item = item,
                        onPause = { downloadViewModel.pauseDownload(it.downloadId) },
                        onResume = { downloadViewModel.resumeDownload(it.downloadId, File(it.savePath).parentFile!!) },
                        onCancel = { downloadViewModel.cancelDownload(it.downloadId) },
                        onRetry = { downloadViewModel.retryDownload(it.downloadId) },
                        onRemove = { downloadViewModel.removeDownload(it.downloadId) }
                    )
                }
            }
        }
    }
}

@Composable
fun DownloadItemCard(
    item: DownloadItem,
    onPause: (DownloadItem) -> Unit,
    onResume: (DownloadItem) -> Unit,
    onCancel: (DownloadItem) -> Unit,
    onRetry: (DownloadItem) -> Unit,
    onRemove: (DownloadItem) -> Unit,
    modifier: Modifier = Modifier
) {
    Card(modifier = modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(16.dp)) {
            // 文件名
            Text(
                text = item.fileName,
                style = MaterialTheme.typography.titleMedium
            )

            Spacer(modifier = Modifier.height(4.dp))

            // 文件大小和速度
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Text(
                    text = formatFileSize(item.downloadedSize) + " / " + formatFileSize(item.totalSize),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )

                if (item.status == DownloadStatus.Downloading && item.speed > 0) {
                    Text(
                        text = formatFileSize(item.speed) + "/s",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary
                    )
                }
            }

            Spacer(modifier = Modifier.height(8.dp))

            // 进度条
            LinearProgressIndicator(
                progress = { item.progress / 100f },
                modifier = Modifier.fillMaxWidth()
            )

            Spacer(modifier = Modifier.height(4.dp))

            // 进度百分比和状态
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Text(
                    text = "${item.progress}%",
                    style = MaterialTheme.typography.bodySmall
                )

                Text(
                    text = getStatusText(item.status),
                    style = MaterialTheme.typography.bodySmall,
                    color = getStatusColor(item.status)
                )
            }

            // 错误信息
            if (item.status == DownloadStatus.Failed && item.errorMessage != null) {
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    text = "错误: ${item.errorMessage}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error
                )
            }

            Spacer(modifier = Modifier.height(8.dp))

            // 操作按钮
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically
            ) {
                when (item.status) {
                    DownloadStatus.Downloading -> {
                        IconButton(onClick = { onPause(item) }) {
                            Icon(Icons.Default.Pause, contentDescription = "暂停")
                        }
                        IconButton(onClick = { onCancel(item) }) {
                            Icon(Icons.Default.Close, contentDescription = "取消")
                        }
                    }
                    DownloadStatus.Paused -> {
                        IconButton(onClick = { onResume(item) }) {
                            Icon(Icons.Default.PlayArrow, contentDescription = "继续")
                        }
                        IconButton(onClick = { onCancel(item) }) {
                            Icon(Icons.Default.Close, contentDescription = "取消")
                        }
                    }
                    DownloadStatus.Failed -> {
                        IconButton(onClick = { onRetry(item) }) {
                            Icon(Icons.Default.Refresh, contentDescription = "重试")
                        }
                        IconButton(onClick = { onRemove(item) }) {
                            Icon(Icons.Default.Delete, contentDescription = "删除")
                        }
                    }
                    DownloadStatus.Completed -> {
                        IconButton(onClick = { onRemove(item) }) {
                            Icon(Icons.Default.Delete, contentDescription = "删除")
                        }
                    }
                    else -> {}
                }
            }
        }
    }
}

@Composable
fun getStatusText(status: DownloadStatus): String {
    return when (status) {
        DownloadStatus.Waiting -> "等待中"
        DownloadStatus.Downloading -> "下载中"
        DownloadStatus.Paused -> "已暂停"
        DownloadStatus.Completed -> "已完成"
        DownloadStatus.Failed -> "失败"
    }
}

@Composable
fun getStatusColor(status: DownloadStatus): Color {
    return when (status) {
        DownloadStatus.Waiting -> MaterialTheme.colorScheme.onSurfaceVariant
        DownloadStatus.Downloading -> MaterialTheme.colorScheme.primary
        DownloadStatus.Paused -> MaterialTheme.colorScheme.tertiary
        DownloadStatus.Completed -> MaterialTheme.colorScheme.secondary
        DownloadStatus.Failed -> MaterialTheme.colorScheme.error
    }
}

