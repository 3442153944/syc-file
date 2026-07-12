// ui/screen/files/SyncFolderMapScreen.kt
// 职责：同步文件夹映射页。
// 同步文件夹由 Windows 端创建（服务器权威）；本页只做**本设备**的事：
// 给每个 folder 指定本地目录映射 + 本机启用开关。映射存本地（SyncMappingStore），
// 不上传服务器——各设备各自维护自己的映射。
package com.sunyuanling.filesync.ui.screen.files

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.sunyuanling.filesync.api.sync.SyncFolderInfo
import com.sunyuanling.filesync.sync.SyncMapping
import com.sunyuanling.filesync.ui.components.files.ErrorCard
import com.sunyuanling.filesync.ui.viewModel.sync.SyncFolderMapViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SyncFolderMapScreen(navController: NavController) {
    val vm = viewModel<SyncFolderMapViewModel>()
    val state by vm.uiState.collectAsState()
    val mappings by vm.mappings.collectAsState()
    var editing by remember { mutableStateOf<SyncFolderInfo?>(null) }

    LaunchedEffect(Unit) { vm.refresh() }

    // 编辑映射路径对话框
    editing?.let { folder ->
        var path by remember(folder.id) {
            mutableStateOf(mappings[folder.id]?.localPath ?: vm.defaultPathFor(folder))
        }
        AlertDialog(
            onDismissRequest = { editing = null },
            title = { Text("本地目录映射") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(folder.name.ifEmpty { folder.remotePath }, fontWeight = FontWeight.Medium)
                    OutlinedTextField(
                        value = path,
                        onValueChange = { path = it },
                        label = { Text("本地目录绝对路径") },
                        modifier = Modifier.fillMaxWidth()
                    )
                    TextButton(onClick = { path = vm.defaultPathFor(folder) }) {
                        Text("使用默认路径", fontSize = 13.sp)
                    }
                }
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        if (path.isNotBlank()) {
                            vm.setMapping(folder.id, path.trim(), enabled = true)
                        }
                        editing = null
                    }
                ) { Text("保存") }
            },
            dismissButton = {
                TextButton(onClick = { editing = null }) { Text("取消") }
            }
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("同步文件夹") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    IconButton(onClick = { vm.refresh() }, enabled = !state.loading) {
                        Icon(Icons.Default.Refresh, contentDescription = "刷新")
                    }
                }
            )
        }
    ) { innerPadding ->
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
                .padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            item {
                Text(
                    text = "同步文件夹由桌面端创建；这里只设置本设备的本地目录映射与启用状态。",
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.outline,
                    modifier = Modifier.padding(vertical = 8.dp)
                )
            }
            if (state.loading) {
                item {
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(16.dp),
                        horizontalArrangement = Arrangement.Center
                    ) { CircularProgressIndicator() }
                }
            }
            state.error?.let { item { ErrorCard(message = it) } }
            if (!state.loading && state.folders.isEmpty()) {
                item {
                    Text(
                        text = "暂无同步文件夹，请先在桌面端创建",
                        fontSize = 14.sp,
                        color = MaterialTheme.colorScheme.outline,
                        modifier = Modifier.padding(vertical = 24.dp)
                    )
                }
            }
            items(state.folders, key = { it.id }) { folder ->
                FolderMapCard(
                    folder = folder,
                    mapping = mappings[folder.id],
                    onEdit = { editing = folder },
                    onToggle = { enabled ->
                        val m = mappings[folder.id]
                        if (m != null) {
                            vm.setMapping(folder.id, m.localPath, enabled)
                        } else if (enabled) {
                            // 未映射时打开即先设默认路径
                            vm.setMapping(folder.id, vm.defaultPathFor(folder), true)
                        }
                    }
                )
            }
            item { Text("", modifier = Modifier.padding(bottom = 8.dp)) }
        }
    }
}

@Composable
private fun FolderMapCard(
    folder: SyncFolderInfo,
    mapping: SyncMapping?,
    onEdit: () -> Unit,
    onToggle: (Boolean) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = folder.name.ifEmpty { folder.remotePath.substringAfterLast('/').substringAfterLast('\\') },
                        fontWeight = FontWeight.Medium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    Text(
                        text = "远端 ${folder.remotePath} · ${directionLabel(folder.direction)}" +
                                if (!folder.enabled) " · 服务端已停用" else "",
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.outline,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
                Switch(
                    checked = mapping?.enabled == true,
                    onCheckedChange = onToggle,
                    enabled = folder.enabled
                )
            }
            HorizontalDivider()
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = mapping?.localPath ?: "未映射本地目录",
                    fontSize = 12.sp,
                    color = if (mapping == null) MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.outline,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                TextButton(onClick = onEdit) { Text("设置", fontSize = 13.sp) }
            }
        }
    }
}

private fun directionLabel(direction: String): String = when (direction) {
    "two_way" -> "双向"
    "upload_only" -> "仅上传"
    "download_only" -> "仅下载"
    else -> direction
}
