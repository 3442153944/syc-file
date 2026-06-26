package com.sunyuanling.filesync.ui.viewModel.monitor

import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.api.user.UserApi
import com.sunyuanling.filesync.api.ws.ConnectionInfo
import com.sunyuanling.filesync.api.ws.OnlineUserInfo
import com.sunyuanling.filesync.api.ws.WsApi
import com.sunyuanling.filesync.ui.viewModel.user.UserStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.text.SimpleDateFormat
import java.util.Locale

/**
 * 设备监控 ViewModel。
 * - 所有用户：加载"我的在线设备"（GET /ws/my-devices）。
 * - 管理员：额外加载"所有在线设备"（GET /ws/online，后端按 admin 守卫）。
 * 自动在 WS 连接成功后刷新，并确保 UserStore 已填充（用于判定 admin）。
 */
class DeviceMonitorViewModel : ViewModel() {

    private val _myDevices = MutableStateFlow<List<DeviceDisplayItem>>(emptyList())
    val myDevices: StateFlow<List<DeviceDisplayItem>> = _myDevices.asStateFlow()

    private val _allDevices = MutableStateFlow<List<DeviceDisplayItem>>(emptyList())
    val allDevices: StateFlow<List<DeviceDisplayItem>> = _allDevices.asStateFlow()

    private val _isAdmin = MutableStateFlow(false)
    val isAdmin: StateFlow<Boolean> = _isAdmin.asStateFlow()

    private val _loading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = _loading.asStateFlow()

    private val _errorMessage = MutableStateFlow<String?>(null)
    val errorMessage: StateFlow<String?> = _errorMessage.asStateFlow()

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

    /**
     * 刷新：先确保 UserStore 有当前用户（用于 admin 判定），再按角色拉取。
     */
    fun refresh() {
        viewModelScope.launch {
            _loading.value = true
            _errorMessage.value = null
            try {
                ensureUserStore()
                _isAdmin.value = UserStore.isAdmin
                loadMyDevices()
                if (UserStore.isAdmin) loadAllDevices() else _allDevices.value = emptyList()
            } catch (e: Exception) {
                Log.e(TAG, "刷新设备列表失败", e)
                _errorMessage.value = e.message ?: "刷新失败"
            } finally {
                _loading.value = false
            }
        }
    }

    private suspend fun ensureUserStore() {
        if (UserStore.current != null) return
        val result = UserApi.verify()
        result.onSuccess { resp ->
            if (resp.code == 200 && resp.data != null) UserStore.setCurrent(resp.data)
        }.onFailure { Log.w(TAG, "verify 失败: ${it.message}") }
    }

    private suspend fun loadMyDevices() {
        val result = WsApi.getMyDevices()
        result.onSuccess { resp ->
            if (resp.code == 200 && resp.data != null) {
                val me = UserStore.current
                _myDevices.value = resp.data.connections.map {
                    it.toDisplay(username = me?.username ?: "")
                }
            } else {
                _errorMessage.value = resp.message.ifEmpty { "加载我的设备失败" }
            }
        }.onFailure {
            Log.e(TAG, "加载我的设备失败", it)
            _errorMessage.value = it.message ?: "网络请求失败"
        }
    }

    private suspend fun loadAllDevices() {
        val result = WsApi.getOnlineUsers()
        result.onSuccess { resp ->
            if (resp.code == 200 && resp.data != null) {
                _allDevices.value = resp.data.users.flatMap { u: OnlineUserInfo ->
                    (u.connections ?: emptyList()).map { c: ConnectionInfo ->
                        c.toDisplay(username = u.username)
                    }
                }
            } else if (resp.code == 403) {
                // 非管理员不应调用此接口；清空即可
                _allDevices.value = emptyList()
            } else {
                _errorMessage.value = resp.message.ifEmpty { "加载所有设备失败" }
            }
        }.onFailure {
            Log.e(TAG, "加载所有设备失败", it)
            _errorMessage.value = it.message ?: "网络请求失败"
        }
    }

    /**
     * 把后端 ConnectionInfo 映射为展示项，计算平台标签。
     */
    private fun ConnectionInfo.toDisplay(username: String): DeviceDisplayItem {
        val dev = this.device
        val platformRaw = dev?.platform ?: ""
        return DeviceDisplayItem(
            connId = connId,
            userId = userId,
            username = username,
            deviceName = dev?.deviceName?.ifEmpty { dev.deviceType } ?: "未知设备",
            platform = platformRaw,
            platformLabel = platformLabel(platformRaw, dev?.deviceType ?: ""),
            ip = ip,
            connectedAtMillis = parseIsoToMillis(connectedAt),
            appVersion = dev?.appVersion ?: "",
            deviceType = dev?.deviceType ?: ""
        )
    }

    companion object {
        private const val TAG = "DeviceMonitorVM"

        /** 平台 → 中文标签。platform 形如 "android_15" / "harmony" / "windows" / "web"。 */
        fun platformLabel(platform: String, deviceType: String): String {
            val p = platform.lowercase()
            return when {
                p.startsWith("android") -> "Android"
                p.startsWith("harmony") -> "鸿蒙"
                p.startsWith("pc") || p.contains("windows") || p.contains("linux") ||
                    deviceType == "desktop" -> "PC"
                p.startsWith("mac") || p.contains("darwin") -> "PC"
                p.startsWith("web") -> "Web"
                p.startsWith("ios") -> "iOS"
                else -> platform.ifEmpty { deviceType.ifEmpty { "未知" } }
            }
        }

        fun parseIsoToMillis(iso: String): Long {
            if (iso.isBlank()) return 0L
            // 规范化 Go time.Time 的 RFC3339Nano 输出：
            // 1) 末尾 'Z' → "+00:00"
            // 2) 把任意位数的小数秒截断/补齐为 3 位毫秒
            var s = iso.trim()
            if (s.endsWith("Z")) s = s.substring(0, s.length - 1) + "+00:00"
            s = Regex("\\.(\\d+)").replace(s) { m ->
                val frac = m.groupValues[1].take(3).padEnd(3, '0')
                ".$frac"
            }
            val fmts = arrayOf(
                "yyyy-MM-dd'T'HH:mm:ss.SSSXXX", // +08:00
                "yyyy-MM-dd'T'HH:mm:ss.SSSZ",   // +0800
                "yyyy-MM-dd'T'HH:mm:ssXXX",
                "yyyy-MM-dd'T'HH:mm:ssZ",
                "yyyy-MM-dd'T'HH:mm:ss",
            )
            for (f in fmts) {
                try {
                    val sdf = SimpleDateFormat(f, Locale.getDefault())
                    return sdf.parse(s)?.time ?: continue
                } catch (_: Exception) {
                }
            }
            return 0L
        }
    }
}

/**
 * 设备展示项（统一用于"我的设备"与"所有设备"模块）。
 */
data class DeviceDisplayItem(
    val connId: String,
    val userId: Int,
    val username: String,
    val deviceName: String,
    val platform: String,
    val platformLabel: String,
    val ip: String,
    val connectedAtMillis: Long,
    val appVersion: String,
    val deviceType: String,
)
