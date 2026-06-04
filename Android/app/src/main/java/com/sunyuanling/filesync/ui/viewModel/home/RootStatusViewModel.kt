// RootStatusViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.util.RootHelper
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class RootStatusViewModel : ViewModel() {

    private val _status = MutableStateFlow<RootStatus>(RootStatus.Checking)
    val status: StateFlow<RootStatus> = _status.asStateFlow()

    init {
        checkStatus()
    }

    private fun checkStatus() {
        viewModelScope.launch {
            try {
                if (checkShizukuAvailable()) {
                    _status.value = RootStatus.Shizuku
                    return@launch
                }

                val isRooted = RootHelper.isDeviceRooted()
                _status.value = if (isRooted) {
                    val hasAccess = RootHelper.checkRootAccess()
                    if (hasAccess) RootStatus.Granted else RootStatus.Denied
                } else {
                    RootStatus.NotRooted
                }
            } catch (e: Exception) {
                _status.value = RootStatus.Error(e.message ?: "检查失败")
            }
        }
    }

    private suspend fun checkShizukuAvailable(): Boolean {
        // TODO: Shizuku检查
        return false
    }

    fun refresh() {
        checkStatus()
    }
}

sealed class RootStatus {
    object Checking : RootStatus()
    object NotRooted : RootStatus()
    object Granted : RootStatus()
    object Denied : RootStatus()
    object Shizuku : RootStatus()
    data class Error(val message: String) : RootStatus()
}