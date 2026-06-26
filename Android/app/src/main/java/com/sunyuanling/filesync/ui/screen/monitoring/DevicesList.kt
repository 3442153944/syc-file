package com.sunyuanling.filesync.ui.screen.monitoring

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.DevicesOther
import androidx.compose.material.icons.filled.Language
import androidx.compose.material.icons.filled.Phone
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.sunyuanling.filesync.ui.viewModel.monitor.DeviceDisplayItem
import com.sunyuanling.filesync.ui.viewModel.monitor.DeviceMonitorViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DevicesList(
    navController: NavController,
    modifier: Modifier = Modifier,
    vm: DeviceMonitorViewModel = viewModel()
) {
    val myDevices by vm.myDevices.collectAsState()
    val allDevices by vm.allDevices.collectAsState()
    val isAdmin by vm.isAdmin.collectAsState()
    val loading by vm.loading.collectAsState()
    val errorMsg by vm.errorMessage.collectAsState()

    LaunchedEffect(Unit) { vm.refresh() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("设备监控") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    IconButton(onClick = { vm.refresh() }, enabled = !loading) {
                        Icon(Icons.Default.Refresh, contentDescription = "刷新")
                    }
                }
            )
        }
    ) { padding ->
        Box(modifier = modifier.fillMaxSize().padding(padding)) {
            if (loading && myDevices.isEmpty() && allDevices.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp)
                ) {
                    errorMsg?.let {
                        item {
                            Card(modifier = Modifier.fillMaxWidth()) {
                                Text(
                                    it,
                                    modifier = Modifier.padding(12.dp),
                                    color = MaterialTheme.colorScheme.error
                                )
                            }
                        }
                    }

                    // ===== 我的在线设备 =====
                    item {
                        SectionHeader("我的在线设备", myDevices.size)
                    }
                    if (myDevices.isEmpty()) {
                        item { EmptyHint("暂无在线设备") }
                    } else {
                        items(myDevices, key = { "my-${it.connId}" }) { dev ->
                            DeviceCard(dev)
                        }
                    }

                    // ===== 所有在线设备（仅管理员）=====
                    if (isAdmin) {
                        item {
                            Spacer(Modifier.size(8.dp))
                            SectionHeader("所有在线设备", allDevices.size)
                        }
                        if (allDevices.isEmpty()) {
                            item { EmptyHint("暂无在线设备") }
                        } else {
                            items(allDevices, key = { "all-${it.connId}" }) { dev ->
                                DeviceCard(dev, showUser = true)
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SectionHeader(title: String, count: Int) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(
            title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold
        )
        Text(
            "$count",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary
        )
    }
    HorizontalDivider(modifier = Modifier.padding(top = 4.dp))
}

@Composable
private fun EmptyHint(text: String) {
    Box(Modifier.fillMaxWidth().padding(vertical = 16.dp), contentAlignment = Alignment.Center) {
        Text(text, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun DeviceCard(dev: DeviceDisplayItem, showUser: Boolean = false) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.padding(16.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(
                imageVector = platformIcon(dev.platformLabel),
                contentDescription = null,
                modifier = Modifier.size(40.dp),
                tint = MaterialTheme.colorScheme.primary
            )
            Spacer(Modifier.size(12.dp))
            Column(modifier = Modifier.fillMaxWidth()) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        dev.deviceName,
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.Medium,
                        maxLines = 1
                    )
                    Text(
                        dev.platformLabel,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.primary
                    )
                }
                Spacer(Modifier.size(4.dp))
                val secondary = buildString {
                    if (showUser && dev.username.isNotEmpty()) {
                        append(dev.username)
                        append("  ·  ")
                    }
                    if (dev.ip.isNotEmpty()) append("IP: ${dev.ip}") else append("IP: -")
                    if (dev.appVersion.isNotEmpty()) {
                        append("  ·  v")
                        append(dev.appVersion)
                    }
                }
                Text(
                    secondary,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    "在线时长: ${formatDuration(dev.connectedAtMillis)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}

private fun platformIcon(label: String): ImageVector = when (label) {
    "Android", "鸿蒙", "iOS" -> Icons.Default.Phone
    "PC" -> Icons.Default.Computer
    "Web" -> Icons.Default.Language
    else -> Icons.Default.DevicesOther
}

private fun formatDuration(connectedAtMillis: Long): String {
    if (connectedAtMillis <= 0) return "未知"
    val secs = (System.currentTimeMillis() - connectedAtMillis) / 1000
    if (secs < 0) return "未知"
    val h = secs / 3600
    val m = (secs % 3600) / 60
    val s = secs % 60
    return when {
        h > 0 -> "${h}时${m}分"
        m > 0 -> "${m}分${s}秒"
        else -> "${s}秒"
    }
}
