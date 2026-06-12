// ui/components/files/DiskList.kt
package com.sunyuanling.filesync.ui.components.files

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sunyuanling.filesync.api.file.DiskInfo

/**
 * 加载指示器
 */
@Composable
fun LoadingIndicator(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .padding(32.dp),
        contentAlignment = Alignment.Center
    ) {
        CircularProgressIndicator()
    }
}

/**
 * 错误卡片
 */
@Composable
fun ErrorCard(
    message: String,
    modifier: Modifier = Modifier
) {
    Card(
        modifier = modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.errorContainer
        )
    ) {
        Text(
            text = message,
            modifier = Modifier.padding(16.dp),
            color = MaterialTheme.colorScheme.onErrorContainer
        )
    }
}

/**
 * LazyColumn 扩展：磁盘列表项
 */
fun LazyListScope.diskItems(
    disks: List<DiskInfo>,
    onDiskClick: ((DiskInfo) -> Unit)? = null
) {
    items(disks, key = { it.mountpoint }) { disk ->
        DiskCard(
            disk = disk,
            onClick = onDiskClick?.let { { it(disk) } }
        )
    }
}