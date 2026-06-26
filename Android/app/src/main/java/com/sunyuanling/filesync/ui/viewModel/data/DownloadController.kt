package com.sunyuanling.filesync.ui.viewModel.data

import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log
import androidx.core.content.ContextCompat
import com.downloader.Error
import com.downloader.OnDownloadListener
import com.downloader.PRDownloader
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsMessage
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.api.file.DownloadParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.dataClass.DownloadItem
import com.sunyuanling.filesync.dataClass.DownloadStatus
import com.sunyuanling.filesync.service.DownloadService
import com.sunyuanling.filesync.ui.components.notice.DownloadNotificationHelper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.File

/**
 * 下载控制单例（进程级唯一状态源）。
 * 统一持有下载列表、PRDownloader id 映射、通知 id 映射，避免多个 ViewModel 实例各自持状态
 * 导致跨页列表不可见 / 暂停恢复失效。UI 的 DownloadListViewModel 仅作薄壳委托本类。
 */
object DownloadController {

    private const val TAG = "DownloadController"

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val json = Json { ignoreUnknownKeys = true }

    @Volatile
    private var appContext: Context? = null
    private var wsObserved = false
    private var restored = false

    private val _downloads = MutableStateFlow<List<DownloadItem>>(emptyList())
    val downloads: StateFlow<List<DownloadItem>> = _downloads.asStateFlow()

    // downloadId -> PRDownloader 内部 int id
    private val activeDownloadIds = mutableMapOf<String, Int>()
    // downloadId -> per-file 通知 id（仅用于完成/失败通知）
    private val notificationIds = mutableMapOf<String, Int>()
    private var nextNotificationId = 1000

    fun attach(context: Context) {
        if (appContext == null) {
            appContext = context.applicationContext
            DownloadNotificationHelper.createChannels(appContext!!)
        }
        if (!wsObserved) {
            wsObserved = true
            observeWebSocket()
        }
        // 持久化续传：开关开启时恢复未完成任务（仅恢复一次）
        if (!restored && AppConfig.persistentDownloadEnabled) {
            restored = true
            restorePendingDownloads()
        }
    }

    /**
     * 从本地持久化恢复未完成下载。PRDownloader 的 resume DB 负责断点续传，
     * 这里只需重新 build().start() 同 URL/目录/文件名即可继续。
     */
    private fun restorePendingDownloads() {
        val pending = DownloadStore.load()
        if (pending.isEmpty()) return
        Log.d(TAG, "恢复 ${pending.size} 个未完成下载")
        _downloads.value = pending.map {
            it.copy(status = DownloadStatus.Waiting, speed = 0L)
        }
        ensureServiceRunning()
        pending.forEach { startDownload(it) }
    }

    private fun getNotificationId(downloadId: String): Int =
        notificationIds.getOrPut(downloadId) { nextNotificationId++ }

    private fun observeWebSocket() {
        scope.launch {
            WebSocketManager.messageFlow.filterNotNull().collect { msg ->
                when (msg) {
                    is WsMessage.Text -> handleWs(msg.content)
                    is WsMessage.Binary -> { /* 暂不处理 */ }
                }
            }
        }
    }

    private fun handleWs(content: String) {
        try {
            val obj = json.parseToJsonElement(content).jsonObject
            val type = obj["type"]?.jsonPrimitive?.content ?: return
            if (type == "file_download") {
                val data = obj["data"]?.jsonObject ?: return
                when (data["event"]?.jsonPrimitive?.content) {
                    "start" -> Log.d(TAG, "服务端通知开始下载: ${data["file_name"]?.jsonPrimitive?.content}")
                    "completed" -> Log.d(TAG, "服务端通知下载完成: ${data["file_name"]?.jsonPrimitive?.content}")
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "处理 WebSocket 消息失败", e)
        }
    }

    /**
     * 添加下载任务。deviceId 为远端设备 id，写入 item 供 startDownload/retry 复用。
     */
    fun addDownload(path: String, name: String, saveDir: File, deviceId: String? = null) {
        val tempId = System.currentTimeMillis().toString()
        val item = DownloadItem(
            downloadId = tempId,
            fileName = name,
            filePath = path,
            savePath = File(saveDir, name).absolutePath,
            totalSize = 0L,
            downloadedSize = 0L,
            progress = 0,
            speed = 0L,
            status = DownloadStatus.Waiting,
            mimeType = "application/octet-stream",
            deviceId = deviceId,
            historyId = null,
            createTime = System.currentTimeMillis(),
            updateTime = System.currentTimeMillis()
        )
        _downloads.value = _downloads.value + item
        persistIfEnabled()
        File(item.savePath).parentFile?.mkdirs()
        ensureServiceRunning()
        startDownload(item)
    }

    private fun startDownload(item: DownloadItem) {
        scope.launch {
            try {
                val url = FileApi.buildDownloadUrl(
                    DownloadParams(path = item.filePath, name = item.fileName, deviceId = item.deviceId ?: "")
                )
                Log.d(TAG, "下载 URL: $url")
                File(item.savePath).parentFile?.mkdirs()

                // 瞬时速度计算状态（修原用 createTime 的平均速度 bug）
                var lastUpdate = 0L
                var lastBytes = 0L
                var lastSpeedTime = 0L

                val prId = PRDownloader.download(
                    url,
                    File(item.savePath).parent,
                    File(item.savePath).name
                )
                    .build()
                    .setOnStartOrResumeListener {
                        val cur = current(item.downloadId) ?: item
                        update(item.downloadId) {
                            it.copy(status = DownloadStatus.Downloading, updateTime = now())
                        }
                        // 恢复/启动时重置速度基准，避免暂停时长被计入
                        lastSpeedTime = System.currentTimeMillis()
                        lastBytes = cur.downloadedSize
                    }
                    .setOnProgressListener { p ->
                        val n = System.currentTimeMillis()
                        if (n - lastUpdate < 200) return@setOnProgressListener
                        lastUpdate = n
                        val pct =
                            if (p.totalBytes > 0) ((p.currentBytes * 100) / p.totalBytes).toInt() else 0
                        val dBytes = p.currentBytes - lastBytes
                        val dMs = n - lastSpeedTime
                        val speed = if (dMs > 0) (dBytes * 1000) / dMs else 0L
                        lastBytes = p.currentBytes
                        lastSpeedTime = n
                        update(item.downloadId) {
                            it.copy(
                                totalSize = p.totalBytes,
                                downloadedSize = p.currentBytes,
                                progress = pct,
                                speed = if (speed > 0) speed else 0L,
                                status = DownloadStatus.Downloading,
                                updateTime = n
                            )
                        }
                    }
                    .start(object : OnDownloadListener {
                        override fun onDownloadComplete() {
                            Log.d(TAG, "下载完成: ${item.fileName}")
                            update(item.downloadId) {
                                it.copy(
                                    status = DownloadStatus.Completed,
                                    progress = 100,
                                    speed = 0L,
                                    updateTime = now()
                                )
                            }
                            activeDownloadIds.remove(item.downloadId)
                            val ctx = appContext ?: return
                            DownloadNotificationHelper.showComplete(
                                ctx,
                                getNotificationId(item.downloadId),
                                item.fileName,
                                current(item.downloadId)?.totalSize ?: 0L,
                                item.savePath
                            )
                        }

                        override fun onError(error: Error) {
                            val msg = error.serverErrorMessage
                                ?: error.connectionException?.message
                                ?: "下载失败"
                            Log.e(TAG, "下载失败: ${item.fileName}, $msg")
                            update(item.downloadId) {
                                it.copy(
                                    status = DownloadStatus.Failed,
                                    errorMessage = msg,
                                    speed = 0L,
                                    updateTime = now()
                                )
                            }
                            activeDownloadIds.remove(item.downloadId)
                            val ctx = appContext ?: return
                            DownloadNotificationHelper.showFailed(
                                ctx,
                                getNotificationId(item.downloadId),
                                item.fileName,
                                msg
                            )
                        }
                    })

                activeDownloadIds[item.downloadId] = prId
            } catch (e: Exception) {
                Log.e(TAG, "启动下载失败: ${item.fileName}", e)
                val msg = e.message ?: "启动下载失败"
                update(item.downloadId) {
                    it.copy(status = DownloadStatus.Failed, errorMessage = msg, updateTime = now())
                }
                val ctx = appContext
                if (ctx != null) {
                    DownloadNotificationHelper.showFailed(
                        ctx, getNotificationId(item.downloadId), item.fileName, msg
                    )
                }
            }
        }
    }

    /**
     * 暂停下载。不再移除通知 id 映射（修原 bug：移除导致恢复后双通知）。
     */
    fun pauseDownload(downloadId: String) {
        activeDownloadIds[downloadId]?.let { PRDownloader.pause(it) }
        update(downloadId) {
            it.copy(status = DownloadStatus.Paused, speed = 0L, updateTime = now())
        }
        Log.d(TAG, "暂停下载: $downloadId")
    }

    /**
     * 恢复下载。不再手动 showProgress（交给 onStartOrResume + Service flow），避免进度被重置为 0。
     */
    fun resumeDownload(downloadId: String) {
        val item = current(downloadId) ?: return
        if (item.status != DownloadStatus.Paused && item.status != DownloadStatus.Waiting) {
            Log.w(TAG, "只能恢复暂停/等待的下载")
            return
        }
        ensureServiceRunning()
        activeDownloadIds[downloadId]?.let { PRDownloader.resume(it) }
        update(downloadId) {
            it.copy(status = DownloadStatus.Downloading, updateTime = now())
        }
        Log.d(TAG, "恢复下载: $downloadId")
    }

    /**
     * 取消下载并从列表移除。
     */
    fun cancelDownload(downloadId: String) {
        activeDownloadIds.remove(downloadId)?.let { PRDownloader.cancel(it) }
        notificationIds[downloadId]?.let { id ->
            appContext?.let { ctx -> DownloadNotificationHelper.cancel(ctx, id) }
        }
        remove(downloadId)
        Log.d(TAG, "取消下载: $downloadId")
    }

    /**
     * 删除已完成/失败的记录。
     */
    fun removeDownload(downloadId: String) {
        notificationIds.remove(downloadId)?.let { id ->
            appContext?.let { ctx -> DownloadNotificationHelper.cancel(ctx, id) }
        }
        remove(downloadId)
    }

    /**
     * 重试失败的下载。使用 item.deviceId（修原把 historyId 当 deviceId 误用）。
     */
    fun retryDownload(downloadId: String) {
        val item = current(downloadId) ?: return
        if (item.status != DownloadStatus.Failed) {
            Log.w(TAG, "只能重试失败的下载")
            return
        }
        notificationIds.remove(downloadId)?.let { id ->
            appContext?.let { ctx -> DownloadNotificationHelper.cancel(ctx, id) }
        }
        update(downloadId) {
            it.copy(
                status = DownloadStatus.Waiting,
                downloadedSize = 0L,
                progress = 0,
                speed = 0L,
                errorMessage = null,
                updateTime = now()
            )
        }
        ensureServiceRunning()
        startDownload(current(downloadId) ?: return)
    }

    /** 是否还需要 Service 运行（有进行中/暂停/等待任务） */
    fun isServiceNeeded(): Boolean = _downloads.value.any {
        it.status == DownloadStatus.Downloading ||
                it.status == DownloadStatus.Paused ||
                it.status == DownloadStatus.Waiting
    }

    private fun ensureServiceRunning() {
        val ctx = appContext ?: return
        ContextCompat.startForegroundService(ctx, Intent(ctx, DownloadService::class.java))
    }

    private fun update(id: String, fn: (DownloadItem) -> DownloadItem) {
        val cur = _downloads.value
        val idx = cur.indexOfFirst { it.downloadId == id }
        if (idx >= 0) {
            val list = cur.toMutableList()
            list[idx] = fn(list[idx])
            _downloads.value = list
            persistIfEnabled()
        }
    }

    private fun current(id: String) = _downloads.value.firstOrNull { it.downloadId == id }

    private fun remove(id: String) {
        _downloads.value = _downloads.value.filterNot { it.downloadId == id }
        persistIfEnabled()
    }

    /** 持久化未完成（非 Completed/Failed/Canceled）任务的快照。完成/失败不持久化。 */
    private fun persistIfEnabled() {
        if (!AppConfig.persistentDownloadEnabled) return
        val pending = _downloads.value.filter {
            it.status == DownloadStatus.Downloading ||
                    it.status == DownloadStatus.Paused ||
                    it.status == DownloadStatus.Waiting
        }
        if (pending.isEmpty()) DownloadStore.clear() else DownloadStore.save(pending)
    }

    private fun now() = System.currentTimeMillis()
}
