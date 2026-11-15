package com.sunyuanling.filesync.mesh

import android.app.Activity
import android.net.VpnService
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.navigation.NavController
import kotlinx.coroutines.launch
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import android.Manifest
import android.os.Build
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MeshScreen(navController: NavController) {
    val context = LocalContext.current
    val state by MeshController.state.collectAsState()
    val scope = rememberCoroutineScope()
    var error by remember { mutableStateOf("") }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (it.resultCode == Activity.RESULT_OK) MeshController.connect(context)
        else error = "未授权 VPN，组网未启动"
    }
    val beginConnect: () -> Unit = {
        scope.launch {
            if (MeshController.useRootBackend()) { MeshController.connect(context); return@launch }
            val connectivity = context.getSystemService(android.content.Context.CONNECTIVITY_SERVICE) as android.net.ConnectivityManager
            if (connectivity.allNetworks.any { connectivity.getNetworkCapabilities(it)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true }) {
                error = "系统 VPN 已被占用；为保留 Clash，本次未启动 VPN。"
                return@launch
            }
            val intent = VpnService.prepare(context)
            if (intent == null) MeshController.connect(context) else permission.launch(intent)
        }
    }
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (!granted) error = "未允许通知，状态仍可在本页查看；可在系统设置中开启组网通知。"
        beginConnect()
    }
    LaunchedEffect(context) {
        while (isActive) { MeshController.refreshNetwork(context); delay(5_000) }
    }
    Scaffold(topBar = { TopAppBar(title = { Text("P2P 组网") }, navigationIcon = {
        TextButton(onClick = { navController.navigateUp() }) { Text("返回") }
    }) }) { padding ->
        // 内容含 Clash 共用卡片，超过一屏后需要能滚动，否则新卡片看不到。
        Column(Modifier.padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("只路由组网地址。有 root 时使用独立内核接口；无 root 时使用系统 VPN。Clash 已占用系统 VPN 时，请使用桌面组网代理，或在 Clash 中承载组网线路。")
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(state.message, style = MaterialTheme.typography.titleMedium)
                    Text("当前网络：${state.network.ifBlank { "检测中" }}")
                    if (state.backend.isNotBlank()) Text("隧道：${state.backend}")
                    if (state.address.isNotBlank()) Text("组网地址：${state.address}\n接收 ${state.received} B · 发送 ${state.sent} B")
                    Text("直连 ${state.peers.count { it.mode == "lan" || it.mode == "direct" }} · 中继 ${state.peers.count { it.mode == "relay" }} · 未验证 ${state.peers.count { it.mode == "unknown" || it.mode == "probing" }}")
                }
            }
            if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error)
            Button(onClick = {
                error = ""
                // busy 表示正在连接/重试；此时提供取消，不整体禁用。
                if (state.busy || state.running) {
                    scope.launch { MeshController.disconnect() }
                    return@Button
                }
                if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED)
                    notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                else beginConnect()
            }) {
                Text(when {
                    state.busy -> "取消连接"
                    state.running -> "断开组网"
                    else -> "连接组网"
                })
            }
            state.peers.forEach { peer ->
                Card(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text("${peer.address} · ${peer.label}", style = MaterialTheme.typography.titleSmall)
                        Text(peer.device, style = MaterialTheme.typography.bodySmall)
                        Text("端点：${peer.endpoint.ifBlank { "暂无" }}")
                        Text(if (peer.handshake > 0) "最近握手：${((System.currentTimeMillis() - peer.handshake) / 1000).coerceAtLeast(0)} 秒前" else "尚未握手")
                        Text("接收 ${peer.received} B · 发送 ${peer.sent} B")
                    }
                }
            }
            ClashProxyCard()
        }
    }
}
