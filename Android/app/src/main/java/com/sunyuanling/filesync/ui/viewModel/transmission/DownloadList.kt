package com.sunyuanling.filesync.ui.viewModel.transmission

import android.app.Application
import android.os.Build
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.compose.runtime.mutableStateListOf
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.Navigator
import com.downloader.Error
import com.downloader.OnDownloadListener
import com.downloader.PRDownloader
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsMessage
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.api.file.DownloadParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.dataClass.DownloadItem
import com.sunyuanling.filesync.dataClass.DownloadStatus
import com.sunyuanling.filesync.ui.components.notice.DownloadNotificationHelper
import com.sunyuanling.filesync.ui.viewModel.data.DownloadRepository
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.File

@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
class DownloadListViewModel(application: Application) : AndroidViewModel(application) {

    companion object {
        private const val TAG = "DownloadListVM"
    }

    // 下载列表
    private val _downloadList = mutableStateListOf<DownloadItem>()
    val downloadList: List<DownloadItem> = _downloadList

    // 活跃下载任务 Map (downloadId -> PRDownloader downloadId)
    private val activeDownloadIds = mutableMapOf<String, Int>()

    // 通知 ID 映射 (downloadId -> notificationId)
    private val notificationIds = mutableMapOf<String, Int>()
    private var nextNotificationId = 1000

    private val json = Json { ignoreUnknownKeys = true }

    private fun getNotificationId(downloadId: String): Int {
        return notificationIds.getOrPut(downloadId) { nextNotificationId++ }
    }

    init {
        observeWebSocketMessages()
        observeConnectionState()
    }

    /**
     * 监听 WebSocket 消息
     */
    private fun observeWebSocketMessages() {
        viewModelScope.launch {
            WebSocketManager.messageFlow
                .filterNotNull()
                .collect { message ->
                    when (message) {
                        is WsMessage.Text -> handleWebSocketMessage(message.content)
                        is WsMessage.Binary -> { /* 暂不处理 */ }
                    }
                }
        }
    }

    /**
     * 监听 WebSocket 连接状态
     */
    private fun observeConnectionState() {
        viewModelScope.launch {
            WebSocketManager.connectionState.collect { state ->
                when (state) {
                    is WsState.Connected -> {
                        Log.d(TAG, "WebSocket 已连接")
                    }
                    is WsState.Disconnected -> {
                        Log.w(TAG, "WebSocket 断开")
                    }
                    is WsState.Error -> {
                        Log.e(TAG, "WebSocket 错误: ${state.message}")
                    }
                    else -> {}
                }
            }
        }
    }

    /**
     * 处理 WebSocket 消息
     */
    private fun handleWebSocketMessage(content: String) {
        try {
            val jsonElement = json.parseToJsonElement(content).jsonObject
            val type = jsonElement["type"]?.jsonPrimitive?.content

            if (type == "file_download") {
                val data = jsonElement["data"]?.jsonObject ?: return
                val event = data["event"]?.jsonPrimitive?.content

                when (event) {
                    "start" -> {
                        val fileName = data["file_name"]?.jsonPrimitive?.content ?: ""
                        Log.d(TAG, "服务器通知开始下载: $fileName")
                    }
                    "completed" -> {
                        val fileName = data["file_name"]?.jsonPrimitive?.content ?: ""
                        Log.d(TAG, "服务器通知下载完成: $fileName")
                    }
                    else -> Log.d(TAG, "未处理的下载事件: $event")
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "处理 WebSocket 消息失败", e)
        }
    }

    /**
     * 添加下载任务
     */
    fun addDownload(
        path: String,
        name: String,
        saveDir: File,
        deviceId: Int? = null
    ) {
        viewModelScope.launch {
            try {
                // 1. 创建下载项（使用时间戳作为临时 ID）
                val tempId = System.currentTimeMillis().toString()

                val downloadItem = DownloadItem(
                    downloadId = tempId,
                    fileName = name,
                    filePath = path,
                    savePath = File(saveDir, name).absolutePath,
                    totalSize = 0L, // 开始下载后 PRDownloader 会更新
                    downloadedSize = 0L,
                    progress = 0,
                    speed = 0L,
                    status = DownloadStatus.Waiting,
                    mimeType = "application/octet-stream",
                    historyId = null,
                    createTime = System.currentTimeMillis(),
                    updateTime = System.currentTimeMillis()
                )

                _downloadList.add(downloadItem)
                File(downloadItem.savePath).parentFile?.mkdirs()

                // 2. 开始下载
                startDownload(downloadItem, path, name, deviceId)

            } catch (e: Exception) {
                Log.e(TAG, "添加下载任务失败", e)
            }
        }
    }

    /**
     * 开始下载
     */
    private fun startDownload(
        item: DownloadItem,
        path: String,
        name: String,
        deviceId: Int?,

    ) {
        viewModelScope.launch {
            try {

                // 构建 GET URL（带参数）
                val downloadUrl = FileApi.buildDownloadUrl(DownloadParams(
                    path = path,
                    name = name,
                    deviceId = deviceId?.toString() ?: ""
                ))

                Log.d(TAG, "下载 URL: $downloadUrl")

                // 使用 PRDownloader（自动支持断点续传）
                val notificationId = getNotificationId(item.downloadId)
                val context = getApplication<Application>()

                val saveFile = File(item.savePath)
                val parentDir = saveFile.parentFile

                val mkdirResult = parentDir?.mkdirs()

                var lastUpdateTime = 0L

                val prDownloadId = PRDownloader.download(
                    downloadUrl,
                    File(item.savePath).parent,
                    File(item.savePath).name
                )
                    .build()
                    .setOnStartOrResumeListener {
                        Log.d(TAG, "下载启动: ${item.fileName}")
                        updateDownloadItem(item.downloadId) { it.copy(
                            status = DownloadStatus.Downloading,
                            updateTime = System.currentTimeMillis()
                        )}
                        DownloadNotificationHelper.showProgress(
                            context, notificationId,
                            item.fileName, 0, 0, item.totalSize, 0
                        )
                    }

                    .setOnProgressListener { progress ->
                        val now = System.currentTimeMillis()
                        if (now - lastUpdateTime < 200) return@setOnProgressListener  // 200ms 限频
                        lastUpdateTime = now

                        val percent = if (progress.totalBytes > 0) {
                            ((progress.currentBytes * 100) / progress.totalBytes).toInt()
                        } else 0

                        val elapsedSeconds =
                            (System.currentTimeMillis() - item.createTime) / 1000 + 1
                        val speed = progress.currentBytes / elapsedSeconds

                        updateDownloadItem(item.downloadId) {
                            it.copy(
                                totalSize = progress.totalBytes,
                                downloadedSize = progress.currentBytes,
                                progress = (percent.toFloat() / 100f).toInt(),  // 注意你的 progress 是 Float 0-1
                                status = DownloadStatus.Downloading,
                                updateTime = System.currentTimeMillis()
                            )
                        }
                        DownloadNotificationHelper.showProgress(
                            context, notificationId,
                            item.fileName, percent,
                            progress.currentBytes, progress.totalBytes, speed
                        )
                    }
                    .start(object : OnDownloadListener {
                        override fun onDownloadComplete() {
                            Log.d(TAG, "下载完成: ${item.fileName}")
                            val updated = _downloadList.find { it.downloadId == item.downloadId }
                            updateDownloadItem(item.downloadId) { it.copy(
                                status = DownloadStatus.Completed,
                                progress = 100,
                                updateTime = System.currentTimeMillis()
                            )}
                            activeDownloadIds.remove(item.downloadId)
                            DownloadNotificationHelper.showComplete(
                                context, notificationId,
                                item.fileName,
                                updated?.totalSize ?: 0,
                                item.savePath
                            )
                        }

                        override fun onError(error: Error) {
                            Log.e(TAG, "下载失败: ${item.fileName}, ${error.serverErrorMessage ?: error.connectionException?.message}")
                            updateDownloadItem(item.downloadId) { it.copy(
                                status = DownloadStatus.Failed,
                                errorMessage = error.serverErrorMessage ?: error.connectionException?.message ?: "下载失败",
                                updateTime = System.currentTimeMillis()
                            )}
                            activeDownloadIds.remove(item.downloadId)
                            DownloadNotificationHelper.showFailed(
                                context, notificationId,
                                item.fileName,
                                error.serverErrorMessage ?: error.connectionException?.message ?: "下载失败"
                            )
                        }
                    })

                activeDownloadIds[item.downloadId] = prDownloadId

            } catch (e: Exception) {
                Log.e(TAG, "启动下载失败: ${item.fileName}", e)
                updateDownloadItem(item.downloadId) { it.copy(
                    status = DownloadStatus.Failed,
                    errorMessage = e.message ?: "启动下载失败",
                    updateTime = System.currentTimeMillis()
                )}
                val notificationId = getNotificationId(item.downloadId)
                DownloadNotificationHelper.showFailed(
                    getApplication<Application>(), notificationId,
                    item.fileName,
                    e.message ?: "启动下载失败"
                )
            }
        }
    }

    /**
     * 暂停下载
     */
    fun pauseDownload(downloadId: String) {
        activeDownloadIds[downloadId]?.let { prDownloadId ->
            PRDownloader.pause(prDownloadId)
            updateDownloadItem(downloadId) { it.copy(
                status = DownloadStatus.Paused,
                updateTime = System.currentTimeMillis()
            )}
            notificationIds[downloadId]?.let {
                DownloadNotificationHelper.cancel(getApplication<Application>(), it)
                notificationIds.remove(downloadId)
            }
            Log.d(TAG, "暂停下载: $downloadId")
        }
    }

    /**
     * 恢复下载
     */
    fun resumeDownload(downloadId: String, parentFile: File) {
        val item = _downloadList.find { it.downloadId == downloadId } ?: return

        if (item.status != DownloadStatus.Paused) {
            Log.w(TAG, "只能恢复暂停的下载")
            return
        }

        activeDownloadIds[downloadId]?.let { prDownloadId ->
            PRDownloader.resume(prDownloadId)
            updateDownloadItem(downloadId) { it.copy(
                status = DownloadStatus.Downloading,
                updateTime = System.currentTimeMillis()
            )}
            // 恢复时重新显示进度通知
            val notificationId = getNotificationId(downloadId)
            DownloadNotificationHelper.showProgress(
                getApplication<Application>(), notificationId,
                item.fileName, item.progress,
                item.downloadedSize, item.totalSize, 0
            )
            Log.d(TAG, "恢复下载: $downloadId")
        }
    }

    /**
     * 取消下载
     */
    fun cancelDownload(downloadId: String) {
        activeDownloadIds[downloadId]?.let { prDownloadId ->
            PRDownloader.cancel(prDownloadId)
            activeDownloadIds.remove(downloadId)
        }
        notificationIds[downloadId]?.let {
            DownloadNotificationHelper.cancel(getApplication<Application>(), it)
            notificationIds.remove(downloadId)
        }
        _downloadList.removeIf { it.downloadId == downloadId }
        Log.d(TAG, "取消下载: $downloadId")
    }

    /**
     * 删除已完成/失败的下载记录
     */
    fun removeDownload(downloadId: String) {
        notificationIds[downloadId]?.let {
            DownloadNotificationHelper.cancel(getApplication<Application>(), it)
            notificationIds.remove(downloadId)
        }
        _downloadList.removeIf { it.downloadId == downloadId }
    }

    /**
     * 重试失败的下载
     */
    fun retryDownload(downloadId: String) {
        val item = _downloadList.find { it.downloadId == downloadId } ?: return

        if (item.status != DownloadStatus.Failed) {
            Log.w(TAG, "只能重试失败的下载")
            return
        }

        // 清除旧的失败通知
        notificationIds[downloadId]?.let {
            DownloadNotificationHelper.cancel(getApplication<Application>(), it)
            notificationIds.remove(downloadId)
        }

        // 重置状态后重新下载
        updateDownloadItem(downloadId) { it.copy(
            status = DownloadStatus.Waiting,
            downloadedSize = 0L,
            progress = 0,
            errorMessage = null,
            updateTime = System.currentTimeMillis()
        )}

        viewModelScope.launch {
            _downloadList.find { it.downloadId == downloadId }?.let { retryItem ->
                startDownload(retryItem, retryItem.filePath, retryItem.fileName, retryItem.historyId)
            }
        }
    }

    /**
     * 更新下载项
     */
    private fun updateDownloadItem(downloadId: String, update: (DownloadItem) -> DownloadItem) {
        val index = _downloadList.indexOfFirst { it.downloadId == downloadId }
        if (index >= 0) {
            _downloadList[index] = update(_downloadList[index])
            DownloadRepository.updateDownload(_downloadList[index])  // 同步
        }
    }

    /**
     * 清理资源
     */
    override fun onCleared() {
        super.onCleared()
        notificationIds.values.forEach {
            DownloadNotificationHelper.cancel(getApplication<Application>(), it)
        }
        notificationIds.clear()
    }
}