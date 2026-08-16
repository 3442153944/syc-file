// WebSocketManager.kt
package com.example.filesync.data.sync

import android.content.Context
import android.util.Log
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.AppConfig.getWsUrl
import com.sunyuanling.filesync.network.AuthManager
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.util.DeviceInfoUtil
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.Json
import okhttp3.*
import okio.ByteString
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

object WebSocketManager {

    private const val TAG = "WebSocketManager"

    /** WebSocket 服务器地址 */
    val serverUrl get() = AppConfig.getWsUrl()

    /** OkHttp 客户端（专用于 WebSocket） */
    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.SECONDS)
        .writeTimeout(10, TimeUnit.SECONDS)
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    /** 当前 WebSocket 连接 */
    private var webSocket: WebSocket? = null

    /** 是否正在连接中 */
    private val isConnecting = AtomicBoolean(false)

    /** 是否应该自动重连 */
    private val shouldReconnect = AtomicBoolean(false)

    /** 重连协程作用域 */
    private val reconnectScope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    /** 重连任务 */
    private var reconnectJob: Job? = null

    /** 重连次数（仅用于日志/诊断，不再作为停止重连的判据） */
    private var reconnectAttempts = 0

    private var deviceInfoJson: String = ""
    private var deviceQueryParams: String = ""

    /** 连接状态 */
    private val _connectionState = MutableStateFlow<WsState>(WsState.Disconnected)
    val connectionState: StateFlow<WsState> = _connectionState.asStateFlow()

    /** 消息流（StateFlow 只保留最新一条，快速连发会丢；新订阅者用 events） */
    private val _messageFlow = MutableStateFlow<WsMessage?>(null)
    val messageFlow: StateFlow<WsMessage?> = _messageFlow.asStateFlow()

    /**
     * 不丢消息的事件流（缓冲 256 条）：同步引擎等需要逐条处理的订阅者用这个。
     * messageFlow 保留给只关心"最新状态"的旧订阅者。
     */
    private val _events = MutableSharedFlow<WsMessage>(extraBufferCapacity = 256)
    val events: SharedFlow<WsMessage> = _events.asSharedFlow()

    /**
     * 连接到服务器
     * 自动从 Request.getToken() 获取 Token
     */
    /**
     * 连接到服务器，携带设备信息
     */
    fun connect(context: Context) {
        if (isConnecting.get()) return

        shouldReconnect.set(true)
        reconnectAttempts = 0

        // 收集设备信息（只在首次连接时收集）
        val info = DeviceInfoUtil.collect(context)
        deviceInfoJson = Json.encodeToString(info)
        deviceQueryParams = "?device_id=${info.deviceId}" +
                "&device_name=${URLEncoder.encode(info.deviceName, "UTF-8")}" +
                "&device_type=android" +
                "&platform=android_${URLEncoder.encode(info.osVersion, "UTF-8")}" +
                "&app_version=${URLEncoder.encode(info.appVersion, "UTF-8")}"

        reconnectScope.launch {
            val token = Request.getToken()
            if (token.isNullOrBlank()) {
                Log.e(TAG, "Token 为空，无法连接")
                _connectionState.value = WsState.Error("Token 为空")
                return@launch
            }
            doConnect(token)
        }
    }

    /**
     * 执行连接（内部方法）
     */
    private fun doConnect(token: String) {
        if (isConnecting.getAndSet(true)) {
            return
        }

        cleanupOldConnection()

        try {
            _connectionState.value = WsState.Connecting

            // 和 Request 一样，在 Header 中携带 token
            val request = okhttp3.Request.Builder()
                .url(serverUrl + deviceQueryParams)   // ← Query 参数
                .header("Token", token)
                .header("User-Agent", "FileSyncApp/1.0.0")
                .header("X-Device-Info", deviceInfoJson)  // ← 详细信息放 Header
                .build()

            webSocket = client.newWebSocket(request, object : WebSocketListener() {

                override fun onOpen(webSocket: WebSocket, response: Response) {
                    isConnecting.set(false)
                    reconnectAttempts = 0
                    _connectionState.value = WsState.Connected
                    cancelReconnect()
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    val msg = WsMessage.Text(text)
                    _messageFlow.value = msg
                    _events.tryEmit(msg)
                }

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    val msg = WsMessage.Binary(bytes.toByteArray())
                    _messageFlow.value = msg
                    _events.tryEmit(msg)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(1000, null)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    isConnecting.set(false)
                    _connectionState.value = WsState.Disconnected

                    if (shouldReconnect.get()) {
                        scheduleReconnect()
                    }
                }

                override fun onFailure(
                    webSocket: WebSocket,
                    t: Throwable,
                    response: Response?
                ) {
                    isConnecting.set(false)
                    val errorMsg = t.message ?: "未知错误"
                    Log.e(TAG, "连接失败: $errorMsg", t)
                    _connectionState.value = WsState.Error(errorMsg)

                    // token 失效是唯一「重试也没用」的失败：必须停下来，否则 3s 一次砸服务端。
                    // ⚠ 后端拒绝握手时回的是 HTTP 200 + body {"code":401}（不是 101 也不是 401），
                    //   OkHttp 这里拿到的 response.code 是 200，所以必须看 body。
                    if (isUnauthorized(response)) {
                        Log.w(TAG, "WS 握手被拒：token 已失效，停止重连并通知跳转登录")
                        shouldReconnect.set(false)
                        _connectionState.value = WsState.Error("登录已过期")
                        reconnectScope.launch {
                            Request.clearToken()
                            AuthManager.notifyTokenExpired()
                        }
                        return
                    }

                    if (shouldReconnect.get()) {
                        scheduleReconnect()
                    }
                }
            })

        } catch (e: Exception) {
            isConnecting.set(false)
            Log.e(TAG, "连接异常: ${e.message}", e)
            _connectionState.value = WsState.Error(e.message ?: "连接异常")

            if (shouldReconnect.get()) {
                scheduleReconnect()
            }
        }
    }

    /**
     * 安排重连任务
     */
    private fun scheduleReconnect() {
        cancelReconnect()

        // 固定间隔重试，**不做指数退避、不设次数上限**：
        // WS 是同步链路的刚需（task_created 全靠它推），全 App 又只有这一条连接，
        // 退避到 30s 意味着断网恢复后最长要干等半分钟才继续同步，收益为零。
        // 唯一的停机条件是「没有 token」（见下），那时重连也没有意义。
        val delayMs = AppConfig.wsReconnectIntervalMs
        reconnectAttempts++

        reconnectJob = reconnectScope.launch {
            delay(delayMs)

            if (shouldReconnect.get()) {
                // 重连时也使用 Request.getToken()
                val token = Request.getToken()
                if (token.isNullOrBlank()) {
                    Log.e(TAG, "Token 为空，停止重连")
                    shouldReconnect.set(false)
                    _connectionState.value = WsState.Error("Token 已失效")
                    return@launch
                }
                doConnect(token)
            }
        }
    }

    /** 握手失败是不是「未授权」：HTTP 401，或后端惯用的 200 + body `{"code":401}`。 */
    private fun isUnauthorized(response: Response?): Boolean {
        if (response == null) return false
        if (response.code == 401) return true
        val body = runCatching { response.body?.string() }.getOrNull() ?: return false
        return body.contains("\"code\":401") || body.contains("\"code\": 401")
    }

    /**
     * 取消重连任务
     */
    private fun cancelReconnect() {
        reconnectJob?.cancel()
        reconnectJob = null
    }

    /**
     * 清理旧连接
     */
    private fun cleanupOldConnection() {
        webSocket?.let {
            try {
                it.close(1000, "创建新连接")
            } catch (e: Exception) {
                Log.e(TAG, "清理旧连接失败: ${e.message}")
            }
        }
        webSocket = null
    }

    /**
     * 发送文本消息
     */
    fun send(message: String): Boolean {
        val ws = webSocket ?: return false

        return try {
            ws.send(message)
        } catch (e: Exception) {
            Log.e(TAG, "发送消息异常: ${e.message}")
            false
        }
    }

    /**
     * 发送二进制消息
     */
    fun send(data: ByteArray): Boolean {
        val ws = webSocket ?: return false

        return try {
            ws.send(ByteString.of(*data))
        } catch (e: Exception) {
            Log.e(TAG, "发送数据异常: ${e.message}")
            false
        }
    }

    /**
     * 断开连接
     */
    fun disconnect() {
        shouldReconnect.set(false)
        cancelReconnect()
        cleanupOldConnection()
        _connectionState.value = WsState.Disconnected
    }

    /**
     * 检查是否已连接
     */
    fun isConnected(): Boolean {
        return webSocket != null && _connectionState.value == WsState.Connected
    }

    /**
     * 获取当前连接状态
     */
    fun getConnectionState(): WsState {
        return _connectionState.value
    }

    /**
     * 释放资源
     */
    fun release() {
        disconnect()
        reconnectScope.cancel()
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
    }
    /**
     * 获取ws连接实例
     * */
    fun getWebSocket(): WebSocket? {
        return webSocket
    }
}

/**
 * WebSocket 消息类型
 */
sealed class WsMessage {
    data class Text(val content: String) : WsMessage()
    data class Binary(val data: ByteArray) : WsMessage() {
        override fun equals(other: Any?): Boolean {
            if (this === other) return true
            if (javaClass != other?.javaClass) return false
            other as Binary
            return data.contentEquals(other.data)
        }

        override fun hashCode(): Int {
            return data.contentHashCode()
        }
    }
}

/**
 * WebSocket 连接状态
 */
sealed class WsState {
    object Connecting : WsState()
    object Connected : WsState()
    object Disconnected : WsState()
    data class Error(val message: String) : WsState()
}