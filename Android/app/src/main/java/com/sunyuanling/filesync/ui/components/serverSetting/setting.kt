package com.sunyuanling.filesync.ui.components.serverSetting

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.ArrowForwardIos
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.SwapVert
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.router.AboutDestination
import com.sunyuanling.filesync.router.FileSettingsDestination
import com.sunyuanling.filesync.router.LogSettingsDestination
import com.sunyuanling.filesync.router.ServerSettingsDestination
import com.sunyuanling.filesync.router.SyncSettingsDestination
import com.sunyuanling.filesync.router.TransferSettingsDestination

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(navController: NavController) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("设置") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                }
            )
        }
    ) { innerPadding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
                .padding(horizontal = 16.dp)
            //允许滚动
            .verticalScroll(rememberScrollState())
        ) {
            SettingsGroup(label = "连接") {
                SettingsNavItem(
                    icon = Icons.Default.Dns,
                    label = "服务器设置",
                    subtitle = "${if (AppConfig.isHttps) "https" else "http"}://${AppConfig.server}:${AppConfig.port}",
                    onClick = { navController.navigate(ServerSettingsDestination) }
                )
            }
            SettingsGroup(label = "传输") {
                SettingsNavItem(
                    icon = Icons.Default.SwapVert,
                    label = "传输设置",
                    subtitle = "并发数、分片大小、下载目录",
                    onClick = { navController.navigate(TransferSettingsDestination) }
                )
            }
            SettingsGroup(label = "同步") {
                SettingsNavItem(
                    icon = Icons.Default.Sync,
                    label = "同步设置",
                    subtitle = "自动同步、Wi-Fi 限制",
                    onClick = { navController.navigate(SyncSettingsDestination) }
                )
            }
            SettingsGroup(label = "系统") {
                SettingsNavItem(
                    icon = Icons.Default.Description,
                    label = "日志设置",
                    subtitle = "级别、文件大小、保留数量",
                    onClick = { navController.navigate(LogSettingsDestination) }
                )
                SettingsNavItem(
                    icon = Icons.Default.Folder,
                    label = "文件浏览",
                    subtitle = "起始目录、每页数量",
                    onClick = { navController.navigate(FileSettingsDestination) }
                )
                SettingsNavItem(
                    icon = Icons.Default.Info,
                    label = "关于",
                    subtitle = "v${AppConfig.versionName}",
                    onClick = { navController.navigate(AboutDestination) }
                )
            }
        }
    }
}

@Composable
private fun SettingsGroup(label: String, content: @Composable ColumnScope.() -> Unit) {
    Text(
        text = label.uppercase(),
        fontSize = 11.sp,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 16.dp, bottom = 4.dp, start = 4.dp)
    )
    Card(modifier = Modifier.fillMaxWidth()) {
        Column { content() }
    }
}

@Composable
private fun SettingsNavItem(
    icon: ImageVector,
    label: String,
    subtitle: String,
    onClick: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(icon, contentDescription = null, modifier = Modifier.size(20.dp),
            tint = MaterialTheme.colorScheme.primary)
        Spacer(Modifier.width(12.dp))
        Column(modifier = Modifier.weight(1f)) {
            Text(label, fontSize = 15.sp)
            Text(subtitle, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Icon(Icons.AutoMirrored.Filled.ArrowForwardIos, contentDescription = null,
            modifier = Modifier.size(14.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}