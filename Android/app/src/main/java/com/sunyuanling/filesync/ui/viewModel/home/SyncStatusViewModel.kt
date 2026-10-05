package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** 首页服务器状态：尚未确认时显示「连接中」，不能一进来就报连接失败。 */
enum class ServerStatus { Checking, Online, Offline }

class SyncStatusViewModel : ViewModel() {

    private val _serverStatus = MutableStateFlow(ServerStatus.Checking)
    val serverStatus: StateFlow<ServerStatus> = _serverStatus.asStateFlow()

    // 直接复用 WebSocketManager 的连接状态
    val wsState: StateFlow<WsState> = WebSocketManager.connectionState

    init {
        observeWsState()
    }

    private fun observeWsState() {
        viewModelScope.launch {
            WebSocketManager.connectionState.collect { state ->
                when (state) {
                    // WS 握手成功即证明服务器可达且 token 有效，直接算在线。
                    // 以前这里要再拉一次 /ws/stats 才置位：那次请求只发一次、失败不重试，
                    // 会一直卡在「连接失败」，直到下次 WS 断线重连，表现为“要等很久才正常”。
                    is WsState.Connected -> _serverStatus.value = ServerStatus.Online
                    is WsState.Error -> _serverStatus.value = ServerStatus.Offline
                    // Connecting/Disconnected：重连循环在跑，确认前保持「连接中」
                    else -> if (_serverStatus.value != ServerStatus.Online) {
                        _serverStatus.value = ServerStatus.Checking
                    }
                }
            }
        }
    }

    override fun onCleared() {
        super.onCleared()
        WebSocketManager.disconnect()
    }
}

sealed class SyncStatus {
    object Idle : SyncStatus()
    data class Syncing(
        val progress: Int,
        val uploadSpeed: Double,
        val downloadSpeed: Double,
        val activeTaskCount: Int
    ) : SyncStatus()
    object Success : SyncStatus()
    data class Failed(val error: String) : SyncStatus()
}
