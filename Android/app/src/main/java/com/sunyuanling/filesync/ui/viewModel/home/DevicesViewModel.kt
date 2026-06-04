package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class DevicesViewModel : ViewModel() {

    private val _devices = MutableStateFlow<List<ConnectedDevice>>(emptyList())
    val devices: StateFlow<List<ConnectedDevice>> = _devices.asStateFlow()

    private val _onlineCount = MutableStateFlow(0)
    val onlineCount: StateFlow<Int> = _onlineCount.asStateFlow()

    init {
        observeWsState()
    }

    private fun observeWsState() {
        viewModelScope.launch {
            WebSocketManager.connectionState.collect { state ->
                if (state is WsState.Connected) {
                    // WebSocket connected means at least this device is online
                    _onlineCount.value = 1
                    _devices.value = listOf(
                        ConnectedDevice(
                            id = "self",
                            name = "当前设备",
                            ip = "",
                            deviceType = DeviceType.SMARTPHONE,
                            isOnline = true,
                            lastSeen = System.currentTimeMillis()
                        )
                    )
                }
            }
        }
    }

    fun refresh() {}
}

data class ConnectedDevice(
    val id: String,
    val name: String,
    val ip: String,
    val deviceType: DeviceType,
    val isOnline: Boolean,
    val lastSeen: Long
)

enum class DeviceType {
    COMPUTER, SMARTPHONE, TABLET, SERVER, OTHER
}
