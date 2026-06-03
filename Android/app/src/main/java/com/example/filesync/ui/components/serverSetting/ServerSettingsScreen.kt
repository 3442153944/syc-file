package com.example.filesync.ui.components.serverSetting

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.example.filesync.AppConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private data class PingLine(val text: String, val type: PingLineType)

private enum class PingLineType { NORMAL, SUCCESS, WARNING, ERROR }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServerSettingsScreen(navController: NavController) {
    var serverAddress by remember { mutableStateOf(AppConfig.server) }
    var serverPort by remember { mutableStateOf(AppConfig.port.toString()) }
    var httpsEnabled by remember { mutableStateOf(AppConfig.isHttps) }
    var connectTimeout by remember { mutableIntStateOf(AppConfig.connectTimeoutMs / 1000) }
    var readTimeout by remember { mutableIntStateOf(AppConfig.readTimeoutMs / 1000) }
    var wsReconnectInterval by remember { mutableIntStateOf((AppConfig.wsReconnectIntervalMs / 1000).toInt()) }

    var isPinging by remember { mutableStateOf(false) }
    var pingLines by remember { mutableStateOf<List<PingLine>>(emptyList()) }
    var pingResult by remember { mutableStateOf<Boolean?>(null) }
    val scope = rememberCoroutineScope()

    val terminalScrollState = rememberScrollState()

    LaunchedEffect(pingLines.size) {
        terminalScrollState.animateScrollTo(terminalScrollState.maxValue)
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("服务器设置") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    TextButton(onClick = {
                        AppConfig.server = serverAddress.trim()
                        AppConfig.port = serverPort.toIntOrNull() ?: 8891
                        AppConfig.isHttps = httpsEnabled
                        AppConfig.connectTimeoutMs = connectTimeout * 1000
                        AppConfig.readTimeoutMs = readTimeout * 1000
                        AppConfig.wsReconnectIntervalMs = wsReconnectInterval * 1000L
                        ConfigManager.save()
                    }) { Text("保存") }
                }
            )
        }
    ) { innerPadding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
                .padding(16.dp)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            // HTTPS 开关
            Card(modifier = Modifier.fillMaxWidth()) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 12.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text("启用 HTTPS", fontSize = 15.sp)
                    Switch(checked = httpsEnabled, onCheckedChange = { httpsEnabled = it })
                }
            }

            // 服务器地址
            OutlinedTextField(
                value = serverAddress,
                onValueChange = { serverAddress = it },
                label = { Text("服务器地址") },
                placeholder = { Text("ddns.sunyuanling.cn") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )

            // 端口
            OutlinedTextField(
                value = serverPort,
                onValueChange = { if (it.all { c -> c.isDigit() }) serverPort = it },
                label = { Text("端口号") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number)
            )

            // 连接超时
            SettingsSliderItem(
                label = "连接超时",
                subtitle = "HTTP 连接超时时间",
                value = connectTimeout,
                range = 5..60,
                unit = " 秒",
                onValueChange = { connectTimeout = it }
            )

            // 读取超时
            SettingsSliderItem(
                label = "读取超时",
                subtitle = "HTTP 读取超时时间",
                value = readTimeout,
                range = 5..60,
                unit = " 秒",
                onValueChange = { readTimeout = it }
            )

            // WebSocket 重连间隔
            SettingsSliderItem(
                label = "WebSocket 重连间隔",
                subtitle = "断线后重试的等待时间",
                value = wsReconnectInterval,
                range = 1..30,
                unit = " 秒",
                onValueChange = { wsReconnectInterval = it }
            )

            // 测试按钮
            OutlinedButton(
                onClick = {
                    scope.launch {
                        isPinging = true
                        pingLines = emptyList()
                        pingResult = null
                        val host = serverAddress.trim()
                        val port = serverPort.toIntOrNull() ?: 8891
                        val lines = mutableListOf<PingLine>()
                        val newLines = testConnection(host, port, httpsEnabled)
                        newLines.forEach { line ->
                            lines.add(line)
                            pingLines = lines.toList()
                        }
                        val hasSuccess = lines.any { it.type == PingLineType.SUCCESS }
                        val hasError = lines.any { it.type == PingLineType.ERROR }
                        pingResult = when {
                            hasSuccess && !hasError -> true
                            !hasSuccess -> false
                            else -> null
                        }
                        isPinging = false
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = !isPinging && serverAddress.isNotBlank()
            ) {
                if (isPinging) {
                    CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(6.dp))
                    Text("测试中...")
                } else {
                    Icon(Icons.Default.Wifi, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("测试连接")
                }
            }

            // Ping 终端
            if (pingLines.isNotEmpty()) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text("连接测试", fontSize = 14.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    when {
                        isPinging -> CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
                        pingResult == true -> Badge(containerColor = Color(0xFF166534)) { Text("连接正常", color = Color(0xFFDCFCE7)) }
                        pingResult == false -> Badge(containerColor = Color(0xFF7F1D1D)) { Text("连接失败", color = Color(0xFFFEE2E2)) }
                        else -> Badge(containerColor = Color(0xFF78350F)) { Text("响应异常", color = Color(0xFFFEF3C7)) }
                    }
                }
                Spacer(Modifier.height(4.dp))
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = 100.dp, max = 300.dp)
                        .background(Color(0xFF1A1A1A), shape = MaterialTheme.shapes.medium)
                        .padding(12.dp)
                ) {
                    SelectionContainer {
                        Column(
                            modifier = Modifier.verticalScroll(terminalScrollState)
                        ) {
                            pingLines.forEach { line ->
                                Text(
                                    text = line.text,
                                    fontFamily = FontFamily.Monospace,
                                    fontSize = 12.sp,
                                    lineHeight = 20.sp,
                                    color = when (line.type) {
                                        PingLineType.NORMAL  -> Color(0xFF888888)
                                        PingLineType.SUCCESS -> Color(0xFF4ADE80)
                                        PingLineType.WARNING -> Color(0xFFFBBF24)
                                        PingLineType.ERROR   -> Color(0xFFF87171)
                                    }
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

private suspend fun runPing(host: String, onLine: suspend (PingLine) -> Unit) {
    withContext(Dispatchers.IO) {
        try {
            val process = Runtime.getRuntime().exec(arrayOf("ping", "-c", "4", "-W", "2", host))
            process.inputStream.bufferedReader().useLines { lines ->
                lines.forEach { raw ->
                    val line = parsePingLine(raw)
                    withContext(Dispatchers.Main) { onLine(line) }
                }
            }
            process.waitFor()
        } catch (e: Exception) {
            withContext(Dispatchers.Main) {
                onLine(PingLine("错误: ${e.message}", PingLineType.ERROR))
            }
        }
    }
}

private suspend fun testConnection(host: String, port: Int, httpsEnabled: Boolean): List<PingLine> {
    return withContext(Dispatchers.IO) {
        val lines = mutableListOf<PingLine>()
        try {
            val protocol = if (httpsEnabled) "https" else "http"
            val url = "$protocol://$host:$port"
            lines.add(PingLine("$ curl $url", PingLineType.NORMAL))

            val start = System.currentTimeMillis()
            val connection = java.net.URL(url).openConnection() as java.net.HttpURLConnection
            connection.connectTimeout = 5000
            connection.readTimeout = 5000
            connection.requestMethod = "GET"
            connection.connect()
            val elapsed = System.currentTimeMillis() - start
            val code = connection.responseCode
            connection.disconnect()

            lines.add(PingLine("HTTP $code  ${elapsed}ms", when {
                code in 200..299 -> PingLineType.SUCCESS
                elapsed > 1000   -> PingLineType.WARNING
                else             -> PingLineType.ERROR
            }))
            lines.add(PingLine("连接成功: $url", PingLineType.SUCCESS))
        } catch (e: Exception) {
            val msg = when {
                e.message?.contains("Unable to parse TLS") == true ->
                    "TLS 握手失败，服务器可能不支持 HTTPS，请尝试关闭 HTTPS 开关"
                e.message?.contains("Connection refused") == true ->
                    "连接被拒绝，请检查端口是否正确"
                e.message?.contains("Unable to resolve host") == true ->
                    "域名解析失败，请检查服务器地址"
                e.message?.contains("timeout") == true ->
                    "连接超时，请检查网络或服务器状态"
                else -> "连接失败: ${e.message}"
            }
            lines.add(PingLine(msg, PingLineType.ERROR))
        }
        lines
    }
}

private fun parsePingLine(raw: String): PingLine {
    return when {
        raw.contains("time=") -> {
            val ms = Regex("time=([\\d.]+)").find(raw)?.groupValues?.get(1)?.toFloatOrNull()
            if (ms != null && ms > 1000f) PingLine(raw, PingLineType.WARNING)
            else PingLine(raw, PingLineType.SUCCESS)
        }
        raw.contains("timeout", ignoreCase = true) ||
                raw.contains("unreachable", ignoreCase = true) ||
                raw.contains("100% packet loss") -> PingLine(raw, PingLineType.ERROR)
        else -> PingLine(raw, PingLineType.NORMAL)
    }
}