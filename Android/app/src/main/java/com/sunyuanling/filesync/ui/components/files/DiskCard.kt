// ui/components/files/DiskCard.kt
package com.sunyuanling.filesync.ui.components.files

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sunyuanling.filesync.api.file.DiskInfo

@Composable
fun DiskCard(
    disk: DiskInfo,
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null
) {
    val usedPercent = (disk.usedPercent / 100.0).coerceIn(0.0, 1.0)
    val animatedProgress by animateFloatAsState(
        targetValue = usedPercent.toFloat(),
        label = "progress"
    )

    val progressColor = when {
        disk.usedPercent >= 90.0 -> Color(0xFFE53935)
        disk.usedPercent >= 70.0 -> Color(0xFFFFA726)
        else -> Color(0xFF66BB6A)
    }

    Card(
        modifier = modifier.fillMaxWidth(),
        elevation = CardDefaults.cardElevation(defaultElevation = 2.dp),
        onClick = { onClick?.invoke() }
    ) {
        Column(
            modifier = Modifier.padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                // 左侧占满剩余宽度；右侧百分比按自身宽度，永远不被挤掉。
                // Linux 的挂载点可以很长（docker overlay、snap 等），所以名称必须单行 + 中间省略，
                // 并且不抢标签的位置：Row 里无 weight 的子项先测量，名称只拿剩下的宽度。
                Column(modifier = Modifier.weight(1f)) {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(
                            text = disk.mountpoint,
                            fontSize = 18.sp,
                            fontWeight = FontWeight.Bold,
                            maxLines = 1,
                            overflow = TextOverflow.MiddleEllipsis,
                            modifier = Modifier.weight(1f, fill = false)
                        )
                        DiskTag(text = disk.fstype)
                        if (disk.isSsd) {
                            DiskTag(
                                text = "SSD",
                                containerColor = Color(0xFF2196F3),
                                contentColor = Color.White
                            )
                        }
                        if (!disk.isAllowed) {
                            DiskTag(
                                text = "禁用",
                                containerColor = Color(0xFF9E9E9E),
                                contentColor = Color.White
                            )
                        }
                    }
                    // 设备名（/dev/sda2、overlay、tmpfs…）：和挂载点一样可能很长，同样单行省略
                    if (disk.device.isNotEmpty() && disk.device != disk.mountpoint) {
                        Text(
                            text = disk.device,
                            fontSize = 12.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.MiddleEllipsis
                        )
                    }
                }
                Text(
                    text = String.format("%.1f%%", disk.usedPercent),
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Medium,
                    color = progressColor,
                    maxLines = 1,
                    softWrap = false
                )
            }

            DiskProgressBar(
                progress = animatedProgress,
                color = progressColor
            )

            // 直接用接口返回的格式化字符串
            // 三段等分宽度、各自单行：窄屏下数字再长也只在自己那一格里省略，不会把别的挤变形
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(4.dp)
            ) {
                Text(
                    text = "已用 ${formatSize(disk.used)}",
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Text(
                    text = "可用 ${disk.freeGb}",
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.weight(1f)
                )
                Text(
                    text = "总计 ${disk.totalGb}",
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    textAlign = TextAlign.End,
                    modifier = Modifier.weight(1f)
                )
            }
        }
    }
}

@Composable
fun DiskTag(
    text: String,
    containerColor: Color = MaterialTheme.colorScheme.secondaryContainer,
    contentColor: Color = MaterialTheme.colorScheme.onSecondaryContainer
) {
    // fstype 也可能很怪（fuse.gvfsd-fuse、fuse.portal…），限宽 + 单行，别把整行撑开
    Surface(
        shape = RoundedCornerShape(4.dp),
        color = containerColor,
        modifier = Modifier.widthIn(max = 96.dp)
    ) {
        Text(
            text = text,
            fontSize = 12.sp,
            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
            color = contentColor,
            maxLines = 1,
            softWrap = false,
            overflow = TextOverflow.Ellipsis
        )
    }
}

@Composable
fun DiskProgressBar(
    progress: Float,
    color: Color,
    modifier: Modifier = Modifier
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(8.dp)
            .clip(RoundedCornerShape(4.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
    ) {
        Box(
            modifier = Modifier
                .fillMaxHeight()
                .fillMaxWidth(progress)
                .clip(RoundedCornerShape(4.dp))
                .background(color)
        )
    }
}

fun formatSize(bytes: Long): String {
    val kb = 1024.0
    val mb = kb * 1024
    val gb = mb * 1024
    val tb = gb * 1024

    return when {
        bytes >= tb -> String.format("%.2f TB", bytes / tb)
        bytes >= gb -> String.format("%.2f GB", bytes / gb)
        bytes >= mb -> String.format("%.2f MB", bytes / mb)
        bytes >= kb -> String.format("%.2f KB", bytes / kb)
        else -> "$bytes B"
    }
}