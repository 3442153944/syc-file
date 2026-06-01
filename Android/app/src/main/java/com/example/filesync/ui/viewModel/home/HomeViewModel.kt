package com.example.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.network.Request
import com.example.filesync.ui.viewModel.files.DiskResponse
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

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
