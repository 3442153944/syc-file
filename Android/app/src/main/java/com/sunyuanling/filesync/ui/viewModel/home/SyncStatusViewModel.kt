package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.api.ws.WsApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class SyncStatusViewModel : ViewModel() {

    private val _serverOnline = MutableStateFlow(false)
    val serverOnline: StateFlow<Boolean> = _serverOnline.asStateFlow()

    // 直接复用 WebSocketManager 的连接状态
    val wsState: StateFlow<WsState> = WebSocketManager.connectionState

    init {
        observeWsState()
    }

    private fun observeWsState() {
        viewModelScope.launch {
            WebSocketManager.connectionState.collect { state ->
                when (state) {
                    is WsState.Connected -> {
                        // WS 连上了，顺手拉一次 stats 确认服务器状态
                        fetchStats()
                    }
                    is WsState.Error -> {
                        _serverOnline.value = false
                    }
                    else -> {}
                }
            }
        }
    }

    private suspend fun fetchStats() {
        WsApi.getStats()
            .onSuccess { response ->
                _serverOnline.value = response.code == 200
            }
            .onFailure {
                _serverOnline.value = false
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
