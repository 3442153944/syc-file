// ui/viewModel/files/ActiveDiskViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.files

import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.file.AvailableDisksData
import com.sunyuanling.filesync.api.file.FileApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

class ActiveDiskViewModel : ViewModel() {

    private val _diskData = MutableStateFlow<AvailableDisksData?>(null)
    val diskData: StateFlow<AvailableDisksData?> = _diskData

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
                FileApi.getAvailableDisks()
                    .onSuccess { response ->
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
            } catch (e: Exception) {
                Log.e("DiskVM", "异常", e)
                _error.value = e.message ?: "未知错误"
                _loading.value = false
            }
        }
    }
}