// WebSocketManager.kt
package com.example.filesync.data.sync

import android.util.Log
import com.example.filesync.network.Request
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import okhttp3.*
import okio.ByteString
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

object WebSocketManager {

    private const val TAG = "WebSocketManager"

    /** WebSocket 服务器地址 */
    var serverUrl = "ws://192.168.31.100:8991/v1/ws/connect"
        private set

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

    /** 重连次数 */
    private var reconnectAttempts = 0
    private val maxReconnectAttempts = 5

    /** 连接状态 */
    private val _connectionState = MutableStateFlow<WsState>(WsState.Disconnected)
    val connectionState: StateFlow<WsState> = _connectionState.asStateFlow()

    /** 消息流 */
    private val _messageFlow = MutableStateFlow<WsMessage?>(null)
    val messageFlow: StateFlow<WsMessage?> = _messageFlow.asStateFlow()

    /**
     * 设置服务器地址
     */
    fun setServerUrl(url: String) {
        serverUrl = url
    }

    /**
     * 连接到服务器
     * 自动从 Request.getToken() 获取 Token
     */
    fun connect() {
        if (isConnecting.get()) {
            return
        }

        shouldReconnect.set(true)
        reconnectAttempts = 0

        reconnectScope.launch {
            // 直接使用 Request.getToken()
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
                .url(serverUrl)
                .header("Token", token)  // 大写 Token，和 Request 保持一致
                .header("User-Agent", "FileSyncApp/1.0.0")
                .build()

            webSocket = client.newWebSocket(request, object : WebSocketListener() {

                override fun onOpen(webSocket: WebSocket, response: Response) {
                    isConnecting.set(false)
                    reconnectAttempts = 0
                    _connectionState.value = WsState.Connected
                    cancelReconnect()
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    _messageFlow.value = WsMessage.Text(text)
                }

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    _messageFlow.value = WsMessage.Binary(bytes.toByteArray())
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
        if (reconnectAttempts >= maxReconnectAttempts) {
            Log.e(TAG, "已达到最大重连次数，停止重连")
            shouldReconnect.set(false)
            _connectionState.value = WsState.Error("连接失败，已停止重试")
            return
        }

        cancelReconnect()

        val delayMs = (1500L * (1 shl reconnectAttempts)).coerceAtMost(30000L)
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