package com.sunyuanling.filesync.ui.viewModel.data

import com.sunyuanling.filesync.dataClass.DownloadItem
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

object DownloadRepository {
    private val _activeDownloads = MutableStateFlow<List<DownloadItem>>(emptyList())
    val activeDownloads: StateFlow<List<DownloadItem>> = _activeDownloads.asStateFlow()

    fun updateDownload(item: DownloadItem) {
        val current = _activeDownloads.value.toMutableList()
        val index = current.indexOfFirst { it.downloadId == item.downloadId }
        if (index >= 0) current[index] = item else current.add(0, item)
        _activeDownloads.value = current
    }

    fun removeDownload(downloadId: String) {
        _activeDownloads.value = _activeDownloads.value.filter { it.downloadId != downloadId }
    }
}