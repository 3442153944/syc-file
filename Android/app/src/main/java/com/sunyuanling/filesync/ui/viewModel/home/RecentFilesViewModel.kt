package com.sunyuanling.filesync.ui.viewModel.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.file.DownloadHistoryParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.previewUtil.PreviewType
import com.sunyuanling.filesync.previewUtil.detectPreviewType
import com.sunyuanling.filesync.util.TransferPathStore
import com.sunyuanling.filesync.util.parseServerTime
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
                val request = DownloadHistoryParams(pageNum = 1, pageSize = 5)
                FileApi.getDownloadHistory(request).onSuccess { response ->
                    if (response.code == 200 && response.data != null) {
                        _files.value = response.data.list.map { item ->
                            val name = item.fileName ?: "未知"
                            val startedAt = parseServerTime(item.startedAt ?: item.createdAt)
                            RecentFile(
                                id = item.id.toString(),
                                name = name,
                                // 服务端历史不带路径，路径只在本机的映射表里；
                                // 本机没发起过的记录（别的设备、旧记录）找不到，为空串
                                path = TransferPathStore.find(name, startedAt)?.remotePath ?: "",
                                size = item.fileSize ?: 0L,
                                lastModified = startedAt,
                                fileType = fileTypeOf(name)
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

/** 按扩展名归类，与在线预览的类型判断保持一致。 */
private fun fileTypeOf(name: String): FileType = when (detectPreviewType(name)) {
    PreviewType.IMAGE -> FileType.IMAGE
    PreviewType.VIDEO -> FileType.VIDEO
    PreviewType.AUDIO -> FileType.AUDIO
    PreviewType.PDF, PreviewType.TEXT,
    PreviewType.OFFICE_WORD, PreviewType.OFFICE_EXCEL, PreviewType.OFFICE_PPT -> FileType.DOCUMENT
    PreviewType.UNSUPPORTED -> FileType.OTHER
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
