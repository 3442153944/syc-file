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
    Scaffold(topBar = { TopAppBar(title = { Text("P2P 组网") }, navigationIcon = {
        TextButton(onClick = { navController.navigateUp() }) { Text("返回") }
    }) }) { padding ->
        // 内容含 Clash 共用卡片，超过一屏后需要能滚动，否则新卡片看不到。
        Column(Modifier.padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("只路由组网地址。有 root 时使用独立内核接口；无 root 时使用系统 VPN。Clash 已占用系统 VPN 时，请使用桌面组网代理，或在 Clash 中承载组网线路。")
            Text(state.message)
            if (state.address.isNotBlank()) Text("组网地址：${state.address}\n接收 ${state.received} B · 发送 ${state.sent} B")
            if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error)
            Button(onClick = {
                error = ""
                // busy 表示正在连接/重试；此时提供取消，不整体禁用。
                if (state.busy || state.running) {
                    scope.launch { MeshController.disconnect() }
                    return@Button
                }
                scope.launch {
                    if (MeshController.useRootBackend()) { MeshController.connect(context); return@launch }
                    val connectivity = context.getSystemService(android.content.Context.CONNECTIVITY_SERVICE) as android.net.ConnectivityManager
                    if (connectivity.allNetworks.any { connectivity.getNetworkCapabilities(it)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true }) {
                        error = "系统 VPN 已被占用。为保留 Clash，本次未启动 VPN；可在 Clash 中使用桌面端的 HTTP/SOCKS5 组网代理。"
                        return@launch
                    }
                    val intent = VpnService.prepare(context)
                    if (intent == null) MeshController.connect(context) else permission.launch(intent)
                }
            }) {
                Text(when {
                    state.busy -> "取消连接"
                    state.running -> "断开组网"
                    else -> "连接组网"
                })
            }
            ClashProxyCard()
        }
    }
}
