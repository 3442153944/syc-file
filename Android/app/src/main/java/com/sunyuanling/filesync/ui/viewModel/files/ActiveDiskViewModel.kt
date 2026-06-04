// ui/viewModel/files/ActiveDiskViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.files

import android.util.Log
import androidx.annotation.Keep
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.network.Request
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

class ActiveDiskViewModel : ViewModel() {

    private val _diskData = MutableStateFlow<DiskData?>(null)
    val diskData: StateFlow<DiskData?> = _diskData

    private val _loading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = _loading

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    init {
        loadDisks()
    }

    fun loadDisks() {
        viewModelScope.launch {
            _loading.value = true
            _error.value = null

            try {
                Request.post<DiskResponse>("/file/available-disks") { result ->
                    result.onSuccess { response ->
                        if (response.code == 200 && response.data != null) {
                            _diskData.value = response.data
                            Log.d("DiskVM", "加载成功: ${response.data.total} 个磁盘")
                        } else {
                            _error.value = response.message
                        }
                    }.onFailure { e ->
                        Log.e("DiskVM", "加载磁盘失败", e)
                        _error.value = e.message ?: "未知错误"
                    }
                    _loading.value = false
                }
            } catch (e: Exception) {
                Log.e("DiskVM", "异常", e)
                _error.value = e.message ?: "未知错误"
                _loading.value = false
            }
        }
    }
}

@Serializable
data class DiskResponse(
    val code: Int = 0,
    val message: String = "",
    val data: DiskData? = null
)

@Serializable
@Keep
data class DiskData(
    val total: Int = 0,
    @SerialName("all_disks")
    val allDisks: List<Disk> = emptyList(),
    @SerialName("allowed_count")
    val allowedCount: Int = 0,
    @SerialName("allowed_disks")
    val allowedDisks: List<Disk>? = null
)

@Serializable
data class Disk(
    val path: String = "",
    val mountpoint: String = "",
    val device: String = "",
    val fstype: String = "",
    val total: Long = 0,
    val free: Long = 0,
    val used: Long = 0,
    @SerialName("used_percent")
    val usedPercent: Double = 0.0,
    @SerialName("total_gb")
    val totalGb: String = "",
    @SerialName("free_gb")
    val freeGb: String = "",
    @SerialName("is_allowed")
    val isAllowed: Boolean = false,
    @SerialName("is_accessible")
    val isAccessible: Boolean = false,
    @SerialName("is_ssd")
    val isSsd: Boolean = false
)