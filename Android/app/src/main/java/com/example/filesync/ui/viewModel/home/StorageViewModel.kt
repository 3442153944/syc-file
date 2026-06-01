package com.example.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.network.Request
import com.example.filesync.ui.viewModel.files.DiskResponse
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class StorageViewModel : ViewModel() {

    private val _info = MutableStateFlow(StorageInfo())
    val info: StateFlow<StorageInfo> = _info.asStateFlow()

    private val _loading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = _loading.asStateFlow()

    init {
        loadStorage()
    }

    fun loadStorage() {
        viewModelScope.launch {
            _loading.value = true
            try {
                Request.postSuspend<DiskResponse>("/file/available-disks").onSuccess { response ->
                    if (response.code == 200 && response.data != null) {
                        var totalBytes = 0L
                        var usedBytes = 0L
                        response.data.allDisks.forEach { disk ->
                            totalBytes += disk.total
                            usedBytes += disk.used
                        }
                        _info.value = StorageInfo(totalBytes = totalBytes, usedBytes = usedBytes)
                    }
                }
            } catch (_: Exception) {
            }
            _loading.value = false
        }
    }

    fun refresh() {
        loadStorage()
    }
}

data class StorageInfo(
    val totalBytes: Long = 0L,
    val usedBytes: Long = 0L
) {
    val usedPercentage: Float
        get() = if (totalBytes > 0) usedBytes.toFloat() / totalBytes else 0f

    fun formatSize(bytes: Long): String {
        return when {
            bytes < 1024 -> "$bytes B"
            bytes < 1024 * 1024 -> "%.1f KB".format(bytes / 1024.0)
            bytes < 1024 * 1024 * 1024 -> "%.1f MB".format(bytes / (1024.0 * 1024))
            else -> "%.2f GB".format(bytes / (1024.0 * 1024 * 1024))
        }
    }
}
