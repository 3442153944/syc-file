package com.example.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.network.Request
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class SyncStatusViewModel : ViewModel() {

    private val _status = MutableStateFlow<SyncStatus>(SyncStatus.Idle)
    val status: StateFlow<SyncStatus> = _status.asStateFlow()

    init {
        checkServerStatus()
    }

    private fun checkServerStatus() {
        viewModelScope.launch {
            try {
                val result = Request.postSuspend<Map<String, Any>>("/ping")
                result.onSuccess {
                    _status.value = SyncStatus.Idle
                }.onFailure {
                    _status.value = SyncStatus.Failed("无法连接到服务器")
                }
            } catch (e: Exception) {
                _status.value = SyncStatus.Failed("无法连接到服务器")
            }
        }
    }

    fun startSync() {
        viewModelScope.launch {
            _status.value = SyncStatus.Syncing(
                progress = 0,
                uploadSpeed = 0.0,
                downloadSpeed = 0.0,
                activeTaskCount = 0
            )
        }
    }

    fun stopSync() {
        _status.value = SyncStatus.Idle
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
