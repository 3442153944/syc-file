package com.sunyuanling.filesync.ui.viewModel.home

import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.api.ws.WsApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * 首页/监控面板用的设备概览 ViewModel。
 * 订阅 WS 连接状态，连接成功后拉取 [WsApi.getMyDevices] 获取真实在线设备，
 * 与设备监控页（DeviceMonitorViewModel）同源，保证各处显示一致。
 */
class DevicesViewModel : ViewModel() {

    companion object {
        private const val TAG = "DevicesViewModel"
    }

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
                if (state is WsState.Connected) refresh()
            }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            try {
                val result = WsApi.getMyDevices()
                result.onSuccess { resp ->
                    if (resp.code == 200 && resp.data != null) {
                        _onlineCount.value = resp.data.totalCount
                        _devices.value = resp.data.connections.map { c ->
                            val dev = c.device
                            ConnectedDevice(
                                id = c.connId,
                                name = dev?.deviceName?.ifEmpty { dev.deviceType } ?: "未知设备",
                                ip = c.ip,
                                deviceType = mapDeviceType(dev?.deviceType ?: "", dev?.platform ?: ""),
                                isOnline = true,
                                lastSeen = System.currentTimeMillis()
                            )
                        }
                    }
                }.onFailure {
                    Log.w(TAG, "获取在线设备失败: ${it.message}")
                }
            } catch (e: Exception) {
                Log.e(TAG, "获取在线设备异常", e)
            }
        }
    }

    private fun mapDeviceType(deviceType: String, platform: String): DeviceType {
        val p = (platform.ifEmpty { deviceType }).lowercase()
        return when {
            p.startsWith("android") || p.startsWith("harmony") || p.startsWith("ios") -> DeviceType.SMARTPHONE
            p.contains("windows") || p.contains("linux") || p == "pc" || p == "desktop" || p.contains("mac") -> DeviceType.COMPUTER
            else -> DeviceType.OTHER
        }
    }
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
