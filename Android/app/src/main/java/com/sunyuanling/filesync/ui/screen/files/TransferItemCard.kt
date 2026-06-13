package com.sunyuanling.filesync.ui.screen.files

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Cancel
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.sunyuanling.filesync.ui.viewModel.files.FileTransferItem
import com.sunyuanling.filesync.ui.viewModel.transmission.FileTransferStatus
import com.sunyuanling.filesync.util.formatDate
import com.sunyuanling.filesync.util.formatFileSize
import com.sunyuanling.filesync.util.formatSpeed

@RequiresApi(Build.VERSION_CODES.O)
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun TransferItemCard(
    item: FileTransferItem,
    isMultiSelect: Boolean = false,
    isSelected: Boolean = false,
    onLongClick: () -> Unit = {},
    onClick: () -> Unit = {},
    onRetry: () -> Unit,
    onCancel: () -> Unit,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onRemove: () -> Unit,
) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = onClick,
                onLongClick = onLongClick
            ),
        border = if (isSelected) BorderStroke(2.dp, MaterialTheme.colorScheme.primary) else null,
        colors = CardDefaults.cardColors(
            containerColor = if (isSelected)
                MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.3f)
            else
                MaterialTheme.colorScheme.surface
        )
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
                // 多选模式用 Checkbox，普通模式用文件图标
                if (isMultiSelect) {
                    Checkbox(
                        checked = isSelected,
                        onCheckedChange = { onClick() },
                        modifier = Modifier.size(40.dp)
                    )
                } else {
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
                }

                Spacer(Modifier.width(12.dp))
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        item.name,
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.Medium,
                        maxLines = 1
                    )
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
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

            // 进度条
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
                            Text(formatSpeed(item.speed), style = MaterialTheme.typography.labelSmall)
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

            // 操作按钮：多选模式下隐藏
            if (!isMultiSelect) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.End,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    when {
                        item.status.canRetry -> TextButton(onClick = onRetry) {
                            Icon(Icons.Default.Refresh, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(4.dp))
                            Text("重试")
                        }
                        item.status == FileTransferStatus.PAUSED -> TextButton(onClick = onResume) {
                            Icon(Icons.Default.PlayArrow, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(4.dp))
                            Text("恢复")
                        }
                        item.status.canPause -> TextButton(onClick = onPause) {
                            Icon(Icons.Default.Pause, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(4.dp))
                            Text("暂停")
                        }
                    }
                    if (item.status.canCancel) TextButton(onClick = onCancel) {
                        Icon(Icons.Default.Cancel, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(4.dp))
                        Text("取消")
                    }
                    if (item.status.canDelete) IconButton(onClick = onRemove) {
                        Icon(Icons.Default.Delete, "删除记录")
                    }
                }
            }
        }
    }
}