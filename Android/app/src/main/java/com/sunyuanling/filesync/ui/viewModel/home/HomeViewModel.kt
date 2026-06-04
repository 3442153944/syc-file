package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class HomeViewModel : ViewModel() {

    private val _transferCounts = MutableStateFlow(Pair(0, 0))
    val transferCounts: StateFlow<Pair<Int, Int>> = _transferCounts.asStateFlow()

    fun refresh() {
        // Transfer counts come from WebSocket / download list state
    }

    fun setTransferCounts(downloading: Int, uploading: Int) {
        _transferCounts.value = Pair(downloading, uploading)
    }
}
