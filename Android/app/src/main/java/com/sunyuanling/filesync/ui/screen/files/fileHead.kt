package com.sunyuanling.filesync.ui.screen.files

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Autorenew
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.SwapVert
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.sunyuanling.filesync.router.TransferListDestination

/**
 * 磁盘列表头部
 */
@Composable
fun DiskListHeader(
    title: String = "可用磁盘",
    loading: Boolean = false,
    onRefresh: () -> Unit,
    navController: NavController
   ) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(
            text = title,
            fontSize = 20.sp,
            fontWeight = FontWeight.Bold
        )
        Row {
            IconButton(onClick = {
                navController.navigate(TransferListDestination)
            }) {
                Icon(Icons.Default.SwapVert, contentDescription = "文件传输列表")
            }
            IconButton(
                onClick = onRefresh,
                enabled = !loading
            ) {
                Icon(Icons.Default.Refresh, contentDescription = "刷新")
            }
        }
    }
}