// ui/viewModel/files/FileTransferListViewModel.kt
package com.example.filesync.ui.viewModel.files

import android.os.Build
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.dataClass.DownloadHistoryItem
import com.example.filesync.dataClass.DownloadHistoryRequest
import com.example.filesync.dataClass.DownloadHistoryResponse
import com.example.filesync.network.Request
import com.example.filesync.ui.viewModel.transmission.FileTransferStatus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

class FileTransferListViewModel : ViewModel() {

    private val _transferItems = MutableStateFlow<List<FileTransferItem>>(emptyList())
    val transferItems: StateFlow<List<FileTransferItem>> = _transferItems

    private val _filterStatus = MutableStateFlow<FileTransferStatus?>(null)
    val filterStatus: StateFlow<FileTransferStatus?> = _filterStatus

    private val _sortBy = MutableStateFlow(SortBy.TIME_DESC)
    val sortBy: StateFlow<SortBy> = _sortBy

    private val _isLoading = MutableStateFlow(false)
    val isLoading: StateFlow<Boolean> = _isLoading

    private val _errorMessage = MutableStateFlow<String?>(null)
    val errorMessage: StateFlow<String?> = _errorMessage

    private val _total = MutableStateFlow(0L)
    val total: StateFlow<Long> = _total

    private var currentPage = 1
    private val pageSize = 20
    private var hasMore = true

    init {
        loadTransferHistory()
    }

    /**
     * 加载下载历史（首次/刷新）
     */
    fun loadTransferHistory() {
        currentPage = 1
        hasMore = true
        viewModelScope.launch {
            _isLoading.value = true
            _errorMessage.value = null
            fetchHistory(isLoadMore = false)
            _isLoading.value = false
        }
    }

    /**
     * 加载更多
     */
    fun loadMore() {
        if (!hasMore || _isLoading.value) return
        viewModelScope.launch {
            _isLoading.value = true
            fetchHistory(isLoadMore = true)
            _isLoading.value = false
        }
    }

    private suspend fun fetchHistory(isLoadMore: Boolean) {
        val req = DownloadHistoryRequest(
            pageNum = currentPage,
            pageSize = pageSize
        )

        val result = Request.postSuspend<DownloadHistoryResponse, DownloadHistoryRequest>(
            endpoint = "/file/download-history",
            body = req
        )

        result.onSuccess { response ->
            if (response.code == 200 && response.data != null) {
                val newItems = response.data.list.map { it.toTransferItem() }
                _total.value = response.data.total

                _transferItems.value = if (isLoadMore) {
                    _transferItems.value + newItems
                } else {
                    newItems
                }

                hasMore = _transferItems.value.size < response.data.total
                if (hasMore) currentPage++

                Log.d(TAG, "加载下载历史: ${newItems.size} 条, 总计 ${response.data.total}")
            } else {
                _errorMessage.value = response.message.ifEmpty { "加载失败" }
            }
        }.onFailure { e ->
            Log.e(TAG, "加载下载历史失败: ${e.message}")
            _errorMessage.value = e.message ?: "网络请求失败"
        }
    }

    // ==================== 本地传输管理 ====================

    fun addTransferItem(item: FileTransferItem) {
        _transferItems.value = listOf(item) + _transferItems.value
    }

    fun updateTransferProgress(id: Int, progress: Float, speed: Long) {
        _transferItems.value = _transferItems.value.map { item ->
            if (item.id == id) item.copy(progress = progress, speed = speed) else item
        }
    }

    fun updateTransferStatus(id: Int, status: FileTransferStatus) {
        _transferItems.value = _transferItems.value.map { item ->
            if (item.id == id) item.copy(status = status) else item
        }
    }

    fun removeTransferItem(id: Int) {
        _transferItems.value = _transferItems.value.filter { it.id != id }
    }

    fun clearCompleted() {
        _transferItems.value = _transferItems.value.filter {
            it.status != FileTransferStatus.COMPLETED
        }
    }

    fun clearAll() {
        _transferItems.value = emptyList()
    }

    // ==================== 筛选排序 ====================

    fun setFilter(status: FileTransferStatus?) {
        _filterStatus.value = status
    }

    fun setSortBy(sortBy: SortBy) {
        _sortBy.value = sortBy
    }

    fun getFilteredAndSortedItems(): List<FileTransferItem> {
        var items = _transferItems.value

        _filterStatus.value?.let { status ->
            items = items.filter { it.status == status }
        }

        items = when (_sortBy.value) {
            SortBy.TIME_DESC -> items.sortedByDescending { it.startTime }
            SortBy.TIME_ASC -> items.sortedBy { it.startTime }
            SortBy.NAME_ASC -> items.sortedBy { it.name }
            SortBy.NAME_DESC -> items.sortedByDescending { it.name }
            SortBy.SIZE_ASC -> items.sortedBy { it.size }
            SortBy.SIZE_DESC -> items.sortedByDescending { it.size }
        }

        return items
    }

    // ==================== 传输操作 ====================

    fun retryTransfer(id: Int) {
        updateTransferStatus(id, FileTransferStatus.WAITING)
        // TODO: 重新发起下载
    }

    fun cancelTransfer(id: Int) {
        updateTransferStatus(id, FileTransferStatus.CANCELLED)
    }

    fun pauseTransfer(id: Int) {
        updateTransferStatus(id, FileTransferStatus.PAUSED)
    }

    fun resumeTransfer(id: Int) {
        updateTransferStatus(id, FileTransferStatus.WAITING)
    }

    fun dismissError() {
        _errorMessage.value = null
    }

    companion object {
        private const val TAG = "FileTransferListVM"
    }
}

// ==================== 数据转换 ====================

/**
 * 后端下载状态 → 前端 FileTransferStatus
 */
private fun mapDownloadStatus(status: String): FileTransferStatus {
    return when (status) {
        "pending" -> FileTransferStatus.WAITING
        "downloading" -> FileTransferStatus.TRANSFERRING
        "completed" -> FileTransferStatus.COMPLETED
        "failed" -> FileTransferStatus.FAILED
        "cancelled" -> FileTransferStatus.CANCELLED
        else -> FileTransferStatus.WAITING
    }
}

/**
 * ISO 时间字符串 → 毫秒时间戳
 */
@RequiresApi(Build.VERSION_CODES.O)
private fun parseTimeToMillis(isoTime: String?): Long {
    if (isoTime.isNullOrEmpty()) return 0L
    return try {
        java.time.ZonedDateTime.parse(isoTime).toInstant().toEpochMilli()
    } catch (e: Exception) {
        try {
            java.time.OffsetDateTime.parse(isoTime).toInstant().toEpochMilli()
        } catch (e: Exception) {
            0L
        }
    }
}

/**
 * 后端 DownloadHistoryItem → 前端 FileTransferItem
 */
@RequiresApi(Build.VERSION_CODES.O)
private fun DownloadHistoryItem.toTransferItem(): FileTransferItem {
    val status = mapDownloadStatus(downloadStatus)
    return FileTransferItem(
        id = id.toInt(),
        name = fileName ?: "未知文件",
        size = fileSize ?: 0L,
        isDir = false,
        childrenCount = 0,
        progress = if (status == FileTransferStatus.COMPLETED) 1f else 0f,
        speed = downloadSpeed ?: 0L,
        status = status,
        startTime = parseTimeToMillis(startedAt),
        endTime = parseTimeToMillis(completedAt).takeIf { it > 0 },
        errorMessage = if (status == FileTransferStatus.FAILED) "下载失败" else null
    )
}

// ==================== 数据类 ====================

data class FileTransferItem(
    val id: Int,
    val name: String,
    val size: Long,
    val isDir: Boolean,
    val childrenCount: Int,
    val progress: Float,
    val speed: Long,
    val status: FileTransferStatus,
    val sourcePath: String = "",
    val targetPath: String = "",
    val startTime: Long = System.currentTimeMillis(),
    val endTime: Long? = null,
    val errorMessage: String? = null
)

enum class SortBy(val displayName: String) {
    TIME_DESC("时间 ↓"),
    TIME_ASC("时间 ↑"),
    NAME_ASC("名称 ↑"),
    NAME_DESC("名称 ↓"),
    SIZE_ASC("大小 ↑"),
    SIZE_DESC("大小 ↓")
}
