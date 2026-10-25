package com.sunyuanling.filesync.mesh

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.serialization.json.JsonPrimitive

/** 无 root 时复用 Clash 的 VPN，桌面代理目前仅支持 TCP。 */
@Composable
fun ClashProxyCard() {
    val context = LocalContext.current
    var host by remember { mutableStateOf("192.168.31.100") }
    var port by remember { mutableStateOf("7897") }
    var message by remember { mutableStateOf("") }
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("与 Clash 共用 VPN", style = MaterialTheme.typography.titleMedium)
            Text("先在已组网的 Windows 桌面端启用 LAN HTTP/SOCKS5 代理，再把下面生成的节点和规则合并到 Clash 配置。仅组网 TCP 流量走该代理；UDP 需要 root 独立接口或 Clash 自身支持的 WireGuard 配置。")
            OutlinedTextField(host, { host = it }, label = { Text("桌面端 LAN 地址") }, singleLine = true)
            OutlinedTextField(port, { port = it }, label = { Text("混合代理端口") }, singleLine = true)
            Button(onClick = {
                runCatching {
                    val address = host.trim()
                    val number = port.toIntOrNull() ?: error("端口无效")
                    require(number in 1..65535 && address.isNotBlank() && !address.any(Char::isWhitespace)) { "请填写有效地址和端口" }
                    require(java.net.URI("socks", null, address, number, null, null, null).host != null) { "地址无效" }
                    val snippet = "proxies:\n  - name: FileSyncMesh\n    type: socks5\n    server: ${JsonPrimitive(address)}\n    port: $number\n    udp: false\nrules:\n  - IP-CIDR,172.1.0.0/16,FileSyncMesh,no-resolve\n"
                    (context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager)
                        .setPrimaryClip(ClipData.newPlainText("FileSync Clash mesh", snippet))
                }.onSuccess { message = "已复制组网代理节点和规则；请合并到现有配置，保留原有规则。" }
                    .onFailure { message = it.message ?: "配置无效" }
            }) { Text("复制 Clash 组网配置") }
            if (message.isNotBlank()) Text(message)
        }
    }
}
