package com.sunyuanling.filesync.ui.screen.files

import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.service.QuickShareService
import com.sunyuanling.filesync.share.*
import kotlinx.coroutines.launch
import kotlinx.serialization.json.*

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun QuickShareScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val tasks by QuickShareController.tasks.collectAsState()
    val message by QuickShareController.message.collectAsState()
    var owner by remember { mutableLongStateOf(0L) }
    var history by remember { mutableStateOf<List<ShareLink>>(emptyList()) }
    var page by remember { mutableIntStateOf(1) }
    var total by remember { mutableLongStateOf(0L) }
    var error by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var autoCopy by remember { mutableStateOf(QuickShareController.autoCopy) }
    var onlyQuick by remember { mutableStateOf(true) }
    var revoke by remember { mutableStateOf<ShareLink?>(null) }
    val overlay by QuickShareService.overlay.collectAsState()
    suspend fun api() = ShareTransport(AppConfig.getBaseUrl(), Request.getToken() ?: error("请先登录"), Request.deviceId())
    suspend fun refresh() {
        busy = true
        try {
            owner = QuickShareController.account()
            val data = api().post("/file/share-link/list", buildJsonObject { put("pageNum", page); put("pageSize", 20) })
            val result = Request.json.decodeFromJsonElement<SharePage>(data)
            history = result.list.orEmpty(); total = result.total; error = ""
        } catch (e: Exception) { error = e.message ?: "加载失败" } finally { busy = false }
    }
    LaunchedEffect(page) { refresh() }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) scope.launch {
            try {
                QuickShareService.start(context)
                QuickShareController.importUris(uris)
            } catch (e: Exception) { error = e.message ?: "导入失败" }
        }
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (Settings.canDrawOverlays(context)) QuickShareService.start(context, true)
        else error = "未授予悬浮窗权限，仍可选择文件或通过系统分享发送"
    }
    Scaffold(topBar = { TopAppBar(title = { Text("快传与分享管理") }, navigationIcon = {
        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "返回") }
    }) }) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            item {
                Card(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text("快速分享文件、图片和视频", style = MaterialTheme.typography.titleMedium)
                        Text("支持多线路上传。链接确认可下载后自动复制；有效期沿用服务端快传设置。")
                        Button(onClick = { picker.launch(arrayOf("*/*")) }) { Text("选择文件快传") }
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text("完成后自动复制链接"); Switch(autoCopy, { autoCopy = it; QuickShareController.autoCopy = it })
                        }
                        OutlinedButton(onClick = {
                            if (overlay) QuickShareService.hideOverlay(context)
                            else if (Settings.canDrawOverlays(context)) QuickShareService.start(context, true)
                            else permission.launch(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:${context.packageName}")))
                        }) { Text(if (overlay) "关闭悬浮窗" else "开启悬浮窗") }
                        Text("支持全局拖拽的应用可长按文件拖入悬浮窗；其他应用可使用系统“分享 → FileSync 快传”。",
                            style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
            if (message.isNotBlank()) item { Text(message) }
            if (error.isNotBlank()) item { Text(error, color = MaterialTheme.colorScheme.error) }
            item { Text("本机快传队列", style = MaterialTheme.typography.titleMedium) }
            val visible = tasks.filter { it.owner == owner && it.realm == AppConfig.getConfiguredBaseUrl() }
            if (visible.isEmpty()) item { Text("还没有快传任务") }
            items(visible, key = { it.id }) { task ->
                OutlinedCard(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Text(task.name, style = MaterialTheme.typography.titleSmall)
                        Text("${task.state} · ${task.sent / 1024} / ${task.size / 1024} KB")
                        LinearProgressIndicator(progress = { if (task.size > 0) (task.sent.toFloat() / task.size).coerceIn(0f, 1f) else 0f }, modifier = Modifier.fillMaxWidth())
                        if (task.error.isNotBlank()) Text(task.error, color = MaterialTheme.colorScheme.error)
                        Row {
                            if (task.state == "上传中") TextButton(onClick = { QuickShareController.pause(task.id) }) { Text("暂停") }
                            else if (task.result == null) TextButton(onClick = {
                                QuickShareService.start(context); QuickShareController.start(task.id)
                            }) { Text("继续 / 重试") }
                            task.result?.let { link -> TextButton(onClick = { QuickShareController.copy(link.url(task.base)) }) { Text("复制链接") } }
                            TextButton(onClick = { scope.launch { QuickShareController.remove(task.id) } }) { Text(if (task.result == null) "取消并删除" else "移除记录") }
                        }
                    }
                }
            }
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text("分享历史（服务器）", style = MaterialTheme.typography.titleMedium)
                    TextButton(enabled = !busy, onClick = { scope.launch { refresh() } }) { Text(if (busy) "加载中" else "刷新") }
                }
                Row { FilterChip(onlyQuick, { onlyQuick = true }, { Text("快传") }); Spacer(Modifier.width(8.dp))
                    FilterChip(!onlyQuick, { onlyQuick = false }, { Text("全部分享") }) }
            }
            items(history.filter { !onlyQuick || it.is_quick_share }, key = { "history-${it.share_code}" }) { link ->
                OutlinedCard(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(12.dp)) {
                        Text(link.file_name, style = MaterialTheme.typography.titleSmall)
                        Text("${if (link.is_quick_share) "快传" else "普通分享"} · ${if (link.active) "有效" else if (link.status == 2) "已撤销" else "已过期"}")
                        Text("到期：${link.expire_time}", style = MaterialTheme.typography.bodySmall)
                        Row {
                            TextButton(enabled = link.active, onClick = { QuickShareController.copy(link.url(AppConfig.getBaseUrl())) }) { Text("复制链接") }
                            TextButton(enabled = link.active, onClick = { revoke = link }) { Text("撤销") }
                        }
                    }
                }
            }
            item {
                Row {
                    TextButton(enabled = page > 1 && !busy, onClick = { page-- }) { Text("上一页") }
                    Text("第 $page 页 / 共 $total 条", Modifier.padding(12.dp))
                    TextButton(enabled = page * 20 < total && !busy, onClick = { page++ }) { Text("下一页") }
                }
                Spacer(Modifier.height(24.dp))
            }
        }
    }
    revoke?.let { link -> AlertDialog(onDismissRequest = { revoke = null }, title = { Text("撤销分享") },
        text = { Text("撤销“${link.file_name}”后，链接立即失效。") },
        confirmButton = { TextButton(onClick = { revoke = null; scope.launch {
            try { api().post("/file/share-link/revoke", buildJsonObject { put("share_code", link.share_code) }); refresh() }
            catch (e: Exception) { error = e.message ?: "撤销失败" }
        } }) { Text("撤销") } }, dismissButton = { TextButton(onClick = { revoke = null }) { Text("保留") } }) }
}
