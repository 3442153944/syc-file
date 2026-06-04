// RunningModeBadge.kt
package com.sunyuanling.filesync.ui.components.home

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.sunyuanling.filesync.ui.viewModel.home.RootStatus
import com.sunyuanling.filesync.ui.viewModel.home.RootStatusViewModel

@Composable
fun RunningModeBadge(
    viewModel: RootStatusViewModel = viewModel()
) {
    val status by viewModel.status.collectAsState()

    val (text, icon, containerColor, contentColor) = when (status) {
        RootStatus.Checking -> BadgeStyle(
            "检查中", Icons.Default.Refresh,
            MaterialTheme.colorScheme.surfaceVariant,
            MaterialTheme.colorScheme.onSurfaceVariant
        )
        RootStatus.NotRooted -> BadgeStyle(
            "正常模式", Icons.Default.PhoneAndroid,
            MaterialTheme.colorScheme.secondaryContainer,
            MaterialTheme.colorScheme.onSecondaryContainer
        )
        RootStatus.Granted -> BadgeStyle(
            "Root模式", Icons.Default.Security,
            MaterialTheme.colorScheme.primaryContainer,
            MaterialTheme.colorScheme.onPrimaryContainer
        )
        RootStatus.Denied -> BadgeStyle(
            "权限受限", Icons.Default.Warning,
            MaterialTheme.colorScheme.errorContainer,
            MaterialTheme.colorScheme.onErrorContainer
        )
        RootStatus.Shizuku -> BadgeStyle(
            "Shizuku", Icons.Default.Settings,
            MaterialTheme.colorScheme.tertiaryContainer,
            MaterialTheme.colorScheme.onTertiaryContainer
        )
        is RootStatus.Error -> BadgeStyle(
            "错误", Icons.Default.Error,
            MaterialTheme.colorScheme.errorContainer,
            MaterialTheme.colorScheme.onErrorContainer
        )
    }

    Surface(
        color = containerColor,
        shape = MaterialTheme.shapes.small
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            if (status == RootStatus.Checking) {
                CircularProgressIndicator(
                    modifier = Modifier.size(16.dp),
                    strokeWidth = 2.dp,
                    color = contentColor
                )
            } else {
                Icon(
                    imageVector = icon,
                    contentDescription = null,
                    modifier = Modifier.size(16.dp),
                    tint = contentColor
                )
            }
            Text(
                text = text,
                fontSize = 13.sp,
                fontWeight = FontWeight.Medium,
                color = contentColor
            )
        }
    }
}

private data class BadgeStyle(
    val text: String,
    val icon: ImageVector,
    val containerColor: Color,
    val contentColor: Color
)