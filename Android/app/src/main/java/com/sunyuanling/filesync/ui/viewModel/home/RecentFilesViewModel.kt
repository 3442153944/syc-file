package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.dataClass.DownloadHistoryRequest
import com.sunyuanling.filesync.dataClass.DownloadHistoryResponse
import com.sunyuanling.filesync.network.Request
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class RecentFilesViewModel : ViewModel() {

    private val _files = MutableStateFlow<List<RecentFile>>(emptyList())
    val files: StateFlow<List<RecentFile>> = _files.asStateFlow()

    private val _loading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = _loading.asStateFlow()

    init {
        loadFiles()
    }

    fun loadFiles() {
        viewModelScope.launch {
            _loading.value = true
            try {
                val request = DownloadHistoryRequest(pageNum = 1, pageSize = 5)
                Request.postSuspend<DownloadHistoryResponse, DownloadHistoryRequest>(
                    endpoint = "/file/download-history",
                    body = request
                ).onSuccess { response ->
                    if (response.code == 200 && response.data != null) {
                        _files.value = response.data.list.map { item ->
                            RecentFile(
                                id = item.id.toString(),
                                name = item.fileName ?: "未知",
                                path = "",
                                size = item.fileSize ?: 0L,
                                lastModified = System.currentTimeMillis(),
                                fileType = FileType.OTHER
                            )
                        }
                    }
                }
            } catch (_: Exception) {
            }
            _loading.value = false
        }
    }

    fun refresh() {
        loadFiles()
    }
}

data class RecentFile(
    val id: String,
    val name: String,
    val path: String,
    val size: Long,
    val lastModified: Long,
    val fileType: FileType
)

enum class FileType {
    DOCUMENT, IMAGE, VIDEO, AUDIO, ARCHIVE, FOLDER, OTHER
}
