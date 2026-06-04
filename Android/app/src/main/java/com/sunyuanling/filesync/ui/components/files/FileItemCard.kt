// ui/components/files/FileItemCard.kt
package com.sunyuanling.filesync.ui.components.files

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.automirrored.filled.TextSnippet
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sunyuanling.filesync.ui.viewModel.files.FileItem

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun FileItemCard(
    item: FileItem,
    onClick: () -> Unit,
    onLongClick: (() -> Unit)? = null,
    modifier: Modifier = Modifier
) {
    Card(
        modifier = modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = onClick,
                onLongClick = onLongClick
            ),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp)
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            // 图标
            Icon(
                imageVector = getFileIcon(item),
                contentDescription = null,
                tint = getFileIconColor(item),
                modifier = Modifier.size(32.dp)
            )

            // 文件信息
            Column(
                modifier = Modifier.weight(1f)
            ) {
                Text(
                    text = item.name,
                    fontSize = 15.sp,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    if (item.isDir) {
                        if (item.childrenCount > 0) {
                            Text(
                                text = "${item.childrenCount} 项",
                                fontSize = 12.sp,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    } else {
                        Text(
                            text = formatSize(item.size),
                            fontSize = 12.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    Text(
                        text = formatModTime(item.modTime),
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }

            // 箭头（仅文件夹）或下载图标（文件）
            if (item.isDir) {
                Icon(
                    imageVector = Icons.Default.ChevronRight,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant
                )
            } else {
                Icon(
                    imageVector = Icons.Default.Download,
                    contentDescription = "下载",
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.size(20.dp)
                )
            }
        }
    }
}

fun getFileIcon(item: FileItem): ImageVector {
    if (item.isDir) return Icons.Default.Folder

    return when (item.extension.lowercase()) {
        ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp" -> Icons.Default.Image
        ".mp4", ".avi", ".mkv", ".mov", ".wmv" -> Icons.Default.VideoFile
        ".mp3", ".wav", ".flac", ".aac", ".ogg" -> Icons.Default.AudioFile
        ".pdf" -> Icons.Default.PictureAsPdf
        ".doc", ".docx" -> Icons.Default.Description
        ".xls", ".xlsx" -> Icons.Default.TableChart
        ".ppt", ".pptx" -> Icons.Default.Slideshow
        ".zip", ".rar", ".7z", ".tar", ".gz" -> Icons.Default.FolderZip
        ".txt", ".md", ".log" -> Icons.AutoMirrored.Filled.TextSnippet
        ".html", ".htm", ".css", ".js", ".ts" -> Icons.Default.Code
        ".json", ".xml", ".yaml", ".yml" -> Icons.Default.DataObject
        ".py", ".java", ".kt", ".c", ".cpp", ".go", ".rs" -> Icons.Default.Code
        ".php" -> Icons.Default.Code
        ".exe", ".msi", ".apk" -> Icons.Default.InstallDesktop
        else -> Icons.AutoMirrored.Filled.InsertDriveFile
    }
}

fun getFileIconColor(item: FileItem): Color {
    if (item.isDir) return Color(0xFFFFA726) // 文件夹橙色

    return when (item.extension.lowercase()) {
        ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp" -> Color(0xFF66BB6A)
        ".mp4", ".avi", ".mkv", ".mov", ".wmv" -> Color(0xFFEF5350)
        ".mp3", ".wav", ".flac", ".aac", ".ogg" -> Color(0xFFAB47BC)
        ".pdf" -> Color(0xFFE53935)
        ".doc", ".docx" -> Color(0xFF2196F3)
        ".xls", ".xlsx" -> Color(0xFF4CAF50)
        ".ppt", ".pptx" -> Color(0xFFFF7043)
        ".zip", ".rar", ".7z", ".tar", ".gz" -> Color(0xFF8D6E63)
        ".py" -> Color(0xFF3776AB)
        ".java", ".kt" -> Color(0xFFF89820)
        ".js", ".ts" -> Color(0xFFF7DF1E)
        ".html", ".css" -> Color(0xFFE44D26)
        ".php" -> Color(0xFF777BB4)
        else -> Color(0xFF78909C)
    }
}

fun formatModTime(isoTime: String): String {
    // 简单处理：取日期部分
    return try {
        isoTime.take(10)
    } catch (_: Exception) {
        ""
    }
}