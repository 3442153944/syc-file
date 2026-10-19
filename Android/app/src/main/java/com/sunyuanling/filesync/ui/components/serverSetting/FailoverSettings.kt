package com.sunyuanling.filesync.ui.components.serverSetting

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.sunyuanling.filesync.network.NodeFailover
import kotlinx.coroutines.launch

@Composable
fun FailoverSettings() {
    val state by NodeFailover.status.collectAsState()
    var enabled by remember { mutableStateOf(NodeFailover.enabled) }
    var backups by remember { mutableStateOf(NodeFailover.backups) }
    var error by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("自动灾备与多线路", style = MaterialTheme.typography.titleMedium)
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("自动灾备"); Switch(enabled, { enabled = it; NodeFailover.enabled = it })
            }
            Text("当前入口不可用时自动选择可用备用入口；同一后端的多条线路也可共同上传文件。")
            OutlinedTextField(backups, { backups = it }, Modifier.fillMaxWidth(), label = { Text("备用入口（每行一个，最多 8 个）") },
                placeholder = { Text("https://example.com/file") }, minLines = 3)
            Button(enabled = !state.checking, onClick = {
                runCatching { NodeFailover.backups = backups }.onSuccess {
                    error = ""; scope.launch { NodeFailover.checkNow() }
                }.onFailure { error = it.message.orEmpty() }
            }) { Text("保存并检查入口") }
            Text("当前入口：${state.active}\n${state.message}", style = MaterialTheme.typography.bodySmall)
            if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error)
        }
    }
}
