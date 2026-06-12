// ui/viewModel/files/FileTransferListViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.files

import android.os.Build
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsMessage
import com.sunyuanling.filesync.api.file.DownloadHistoryParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.api.file.DownloadHistoryItem
import com.sunyuanling.filesync.dataClass.DownloadStatus
import com.sunyuanling.filesync.ui.viewModel.data.DownloadRepository
import com.sunyuanling.filesync.ui.viewModel.transmission.FileTransferStatus
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.time.OffsetDateTime
import java.time.ZonedDateTime

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

    private val json = Json { ignoreUnknownKeys = true }

    init {
        loadTransferHistory()
        observeWebSocketDownloads()
        observeActiveDownloads()
    }

    /**
     * 监听 WebSocket 下载事件，实时同步下载记录
     */
    private fun observeWebSocketDownloads() {
        viewModelScope.launch {
            WebSocketManager.messageFlow
                .filterNotNull()
                .collect { message ->
                    if (message is WsMessage.Text) {
                        try {
                            val jsonElement = json.parseToJsonElement(message.content).jsonObject
                            val type = jsonElement["type"]?.jsonPrimitive?.content
                            if (type == "file_download") {
                                val data = jsonElement["data"]?.jsonObject ?: return@collect
                                val event = data["event"]?.jsonPrimitive?.content
                                val fileName = data["file_name"]?.jsonPrimitive?.content ?: ""
                                val fileSize = data["file_size"]?.jsonPrimitive?.content?.toLongOrNull() ?: 0L
                                val historyId = data["history_id"]?.jsonPrimitive?.content?.toIntOrNull() ?: return@collect

                                when (event) {
                                    "start" -> {
                                        // 如果已存在（从 API 加载的），更新状态
                                        val existing = _transferItems.value.find { it.id == historyId }
                                        if (existing != null) {
                                            _transferItems.value = _transferItems.value.map { item ->
                                                if (item.id == historyId) {
                                                    item.copy(status = FileTransferStatus.TRANSFERRING)
                                                } else item
                                            }
                                        } else {
                                            val newItem = FileTransferItem(
                                                id = historyId,
                                                name = fileName,
                                                size = fileSize,
                                                isDir = false,
                                                childrenCount = 0,
                                                progress = 0f,
                                                speed = 0L,
                                                status = FileTransferStatus.TRANSFERRING,
                                                startTime = System.currentTimeMillis()
                                            )
                                            _transferItems.value = listOf(newItem) + _transferItems.value
                                            _total.value = _total.value + 1
                                        }
                                        Log.d("FileTransferListVM", "WebSocket 下载开始: $fileName (id=$historyId)")
                                    }
                                    "completed" -> {
                                        val now = System.currentTimeMillis()
                                        _transferItems.value = _transferItems.value.map { item ->
                                            if (item.id == historyId) {
                                                item.copy(
                                                    status = FileTransferStatus.COMPLETED,
                                                    progress = 1f,
                                                    endTime = now
                                                )
                                            } else item
                                        }
                                        Log.d("FileTransferListVM", "WebSocket 下载完成: $fileName (id=$historyId)")
                                    }
                                }
                            }
                        } catch (e: Exception) {
                            Log.e("FileTransferListVM", "解析 WebSocket 消息失败", e)
                        }
                    }
                }
        }
    }

    private fun observeActiveDownloads() {
        viewModelScope.launch {
            DownloadRepository.activeDownloads.collect { activeList ->
                if (activeList.isEmpty()) return@collect

                val activeItems = activeList.map { download ->
                    FileTransferItem(
                        id = download.downloadId.hashCode(),
                        name = download.fileName,
                        size = download.totalSize,
                        isDir = false,
                        childrenCount = 0,
                        progress = download.progress.toFloat() / 100f,
                        speed = download.speed,
                        status = when (download.status) {
                            DownloadStatus.Waiting -> FileTransferStatus.WAITING
                            DownloadStatus.Downloading -> FileTransferStatus.TRANSFERRING
                            DownloadStatus.Paused -> FileTransferStatus.PAUSED
                            DownloadStatus.Completed -> FileTransferStatus.COMPLETED
                            DownloadStatus.Failed -> FileTransferStatus.FAILED
                        },
                        startTime = download.createTime
                    )
                }

                // 合并：活跃下载优先，历史记录补充
                val activeIds = activeItems.map { it.id }.toSet()
                val historyItems = _transferItems.value.filter { it.id !in activeIds }
                _transferItems.value = activeItems + historyItems
            }
        }
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
        val req = DownloadHistoryParams(
            pageNum = currentPage,
            pageSize = pageSize
        )

        val result = FileApi.getDownloadHistory(req)

        result.onSuccess { response ->
            if (response.code == 200 && response.data != null) {
                val newItems = response.data.list.map { it.toTransferItem() }
                _total.value = response.data.total

                _transferItems.value = if (isLoadMore) {
                    _transferItems.value + newItems
                } else {
                    // 合并 WebSocket 实时添加的记录（避免覆盖）
                    val existingIds = newItems.map { it.id }.toSet()
                    val wsItems = _transferItems.value.filter { it.id !in existingIds }
                    newItems + wsItems
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
private fun parseTimeToMillis(timeStr: String?): Long {
    if (timeStr.isNullOrBlank()) return 0L
    return try {
        java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", java.util.Locale.getDefault())
            .also { it.timeZone = java.util.TimeZone.getTimeZone("UTC") }
            .parse(timeStr)?.time ?: 0L
    } catch (e: Exception) {
        try {
            // 兼容带毫秒的格式
            java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", java.util.Locale.getDefault())
                .also { it.timeZone = java.util.TimeZone.getTimeZone("UTC") }
                .parse(timeStr)?.time ?: 0L
        } catch (e2: Exception) {
            0L
        }
    }
}

/**
 * 后端 DownloadHistoryItem → 前端 FileTransferItem
 */
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
