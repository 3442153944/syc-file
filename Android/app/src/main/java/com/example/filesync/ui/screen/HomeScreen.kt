package com.example.filesync.ui.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.example.filesync.router.AppRoute
import com.example.filesync.router.navigateToDetail
import com.example.filesync.ui.components.home.*
import com.example.filesync.ui.viewModel.home.*

@Composable
fun HomeScreen(
    navController: NavController,
    modifier: Modifier = Modifier,
    homeVM: HomeViewModel = viewModel(),
    storageVM: StorageViewModel = viewModel(),
    devicesVM: DevicesViewModel = viewModel(),
    recentFilesVM: RecentFilesViewModel = viewModel(),
    syncVM: SyncStatusViewModel = viewModel()
) {
    val transferCounts by homeVM.transferCounts.collectAsState()
    val storageInfo by storageVM.info.collectAsState()
    val devices by devicesVM.devices.collectAsState()
    val onlineCount by devicesVM.onlineCount.collectAsState()
    val recentFiles by recentFilesVM.files.collectAsState()
    val syncStatus by syncVM.status.collectAsState()

    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        item {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = "文件同步",
                        fontSize = 28.sp,
                        fontWeight = FontWeight.Bold
                    )
                    Text(
                        text = if (syncStatus is SyncStatus.Failed) "服务器连接失败" else "一切正常运行中",
                        fontSize = 14.sp,
                        color = if (syncStatus is SyncStatus.Failed)
                            MaterialTheme.colorScheme.error
                        else
                            MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                RunningModeBadge()
            }
        }

        // 存储空间
        item {
            OutlinedCard(modifier = Modifier.fillMaxWidth()) {
                Column(
                    modifier = Modifier.padding(20.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp)
                ) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(
                            text = "存储空间",
                            fontSize = 18.sp,
                            fontWeight = FontWeight.SemiBold
                        )
                        IconButton(onClick = { storageVM.refresh() }) {
                            Icon(Icons.Default.Refresh, contentDescription = "刷新")
                        }
                    }

                    if (storageInfo.totalBytes > 0) {
                        LinearProgressIndicator(
                            progress = { storageInfo.usedPercentage },
                            modifier = Modifier.fillMaxWidth(),
                            color = MaterialTheme.colorScheme.primary
                        )

                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Text(
                                text = "已使用 ${storageInfo.formatSize(storageInfo.usedBytes)}",
                                fontSize = 14.sp
                            )
                            Text(
                                text = "共 ${storageInfo.formatSize(storageInfo.totalBytes)}",
                                fontSize = 14.sp,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    } else {
                        Text(
                            text = "加载中...",
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                }
            }
        }

        // 同步状态
        item {
            ElevatedCard(
                modifier = Modifier.fillMaxWidth(),
                colors = CardDefaults.elevatedCardColors(
                    containerColor = MaterialTheme.colorScheme.primaryContainer
                )
            ) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(20.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Column {
                        Text(
                            text = "设备连接",
                            fontSize = 18.sp,
                            fontWeight = FontWeight.SemiBold
                        )
                        Spacer(modifier = Modifier.height(4.dp))
                        Text(
                            text = if (onlineCount > 0) "${onlineCount} 台设备在线" else "暂无设备连接",
                            fontSize = 14.sp,
                            color = if (onlineCount > 0)
                                MaterialTheme.colorScheme.primary
                            else
                                MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    Icon(
                        imageVector = if (onlineCount > 0) Icons.Default.CloudDone else Icons.Default.CloudOff,
                        contentDescription = null,
                        tint = if (onlineCount > 0)
                            MaterialTheme.colorScheme.primary
                        else
                            MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(36.dp)
                    )
                }
            }
        }

        // 在线设备列表
        if (devices.isNotEmpty()) {
            item {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        text = "已连接设备",
                        fontSize = 18.sp,
                        fontWeight = FontWeight.SemiBold
                    )
                    devices.forEach { device ->
                        OutlinedCard(modifier = Modifier.fillMaxWidth()) {
                            Row(
                                modifier = Modifier.padding(16.dp),
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(12.dp)
                            ) {
                                Icon(
                                    imageVector = when (device.deviceType) {
                                        DeviceType.COMPUTER -> Icons.Default.Computer
                                        DeviceType.SMARTPHONE -> Icons.Default.PhoneAndroid
                                        else -> Icons.Default.Devices
                                    },
                                    contentDescription = null,
                                    tint = if (device.isOnline)
                                        MaterialTheme.colorScheme.primary
                                    else
                                        MaterialTheme.colorScheme.onSurfaceVariant
                                )
                                Column(modifier = Modifier.weight(1f)) {
                                    Text(text = device.name, fontWeight = FontWeight.Medium)
                                    Text(
                                        text = if (device.isOnline) "在线" else "离线",
                                        fontSize = 12.sp,
                                        color = if (device.isOnline)
                                            MaterialTheme.colorScheme.primary
                                        else
                                            MaterialTheme.colorScheme.onSurfaceVariant
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }

        // 快速操作
        item {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(
                    text = "快速操作",
                    fontSize = 18.sp,
                    fontWeight = FontWeight.SemiBold
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilledTonalButton(
                        onClick = { navController.navigateToDetail(AppRoute.FileUpload) }
                    ) {
                        Icon(Icons.Default.Upload, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(modifier = Modifier.width(6.dp))
                        Text("上传文件")
                    }
                    FilledTonalButton(
                        onClick = { navController.navigateToDetail(AppRoute.FileSearch) }
                    ) {
                        Icon(Icons.Default.Search, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(modifier = Modifier.width(6.dp))
                        Text("搜索文件")
                    }
                }
            }
        }

        // 最近文件
        item {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    text = "最近下载",
                    fontSize = 18.sp,
                    fontWeight = FontWeight.SemiBold
                )
                if (recentFiles.isEmpty()) {
                    Text(
                        text = "暂无下载记录",
                        fontSize = 14.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                } else {
                    recentFiles.forEach { file ->
                        OutlinedCard(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable {
                                    navController.navigateToDetail(
                                        AppRoute.FileDetail.createRoute(file.id)
                                    )
                                }
                        ) {
                            Row(
                                modifier = Modifier.padding(16.dp),
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(12.dp)
                            ) {
                                Icon(
                                    imageVector = Icons.Default.InsertDriveFile,
                                    contentDescription = null,
                                    tint = MaterialTheme.colorScheme.primary
                                )
                                Column(modifier = Modifier.weight(1f)) {
                                    Text(
                                        text = file.name,
                                        fontWeight = FontWeight.Medium,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis
                                    )
                                    Text(
                                        text = StorageInfo().formatSize(file.size),
                                        fontSize = 12.sp,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }

        item { Spacer(modifier = Modifier.height(16.dp)) }
    }
}
