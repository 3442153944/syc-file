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
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        text = disk.mountpoint,
                        fontSize = 18.sp,
                        fontWeight = FontWeight.Bold
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
                Text(
                    text = String.format("%.1f%%", disk.usedPercent),
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Medium,
                    color = progressColor
                )
            }

            DiskProgressBar(
                progress = animatedProgress,
                color = progressColor
            )

            // 直接用接口返回的格式化字符串
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Text(
                    text = "已用 ${formatSize(disk.used)}",
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    text = "可用 ${disk.freeGb}",
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    text = "总计 ${disk.totalGb}",
                    fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
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
    Surface(
        shape = RoundedCornerShape(4.dp),
        color = containerColor
    ) {
        Text(
            text = text,
            fontSize = 12.sp,
            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
            color = contentColor
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