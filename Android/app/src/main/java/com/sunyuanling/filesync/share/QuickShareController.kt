package com.sunyuanling.filesync.share

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.api.user.UserApi
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.ui.viewModel.user.UserStore
import com.sunyuanling.filesync.util.UploadStaging
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.encodeToString
import java.io.File
import java.util.UUID

/** 界面和悬浮窗共用同一队列；私有暂存与会话落盘，进程退出后由用户选择恢复。 */
object QuickShareController {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val lock = Mutex()
    private lateinit var context: Context
    private val state = MutableStateFlow<List<ShareTask>>(emptyList())
    val tasks = state.asStateFlow()
    private val jobs = java.util.concurrent.ConcurrentHashMap<String, Job>()
    private val ready = CompletableDeferred<Unit>()
    private val uploads = kotlinx.coroutines.sync.Semaphore(2)
    private val messages = MutableStateFlow("")
    val message = messages.asStateFlow()
    private val root get() = File(context.filesDir, "quick-share/files")
    private val queue get() = File(context.filesDir, "quick-share/queue.json")
    var autoCopy: Boolean
        get() = context.getSharedPreferences("quick_share", 0).getBoolean("auto_copy", true)
        set(value) { context.getSharedPreferences("quick_share", 0).edit().putBoolean("auto_copy", value).apply() }

    fun init(app: Context) {
        if (::context.isInitialized) return
        context = app.applicationContext
        scope.launch(Dispatchers.IO) {
            lock.withLock {
                val saved = runCatching { Request.json.decodeFromString<List<ShareTask>>(queue.readText()) }.getOrDefault(emptyList())
                state.value = saved.map { if (it.state in listOf("上传中", "等待", "正在暂存"))
                    it.copy(state = "已暂停", error = "上次任务未结束，可继续上传") else it }
                ready.complete(Unit)
            }
        }
    }

    suspend fun account(): Long {
        ready.await()
        val user = UserApi.verify().getOrThrow().data ?: error("请先登录")
        UserStore.setCurrent(user)
        return user.id.toLong()
    }

    suspend fun importUris(uris: List<Uri>) {
        require(uris.isNotEmpty() && uris.size <= 100) { "每次请选择 1–100 个文件" }
        val owner = account()
        val base = AppConfig.getBaseUrl()
        val realm = AppConfig.getConfiguredBaseUrl()
        for (uri in uris.distinct()) {
            currentCoroutineContext().ensureActive()
            require(uri.scheme == "content") { "仅接收系统授权的文件或媒体 URI" }
            messages.value = "正在暂存文件…"
            val file = withContext(Dispatchers.IO) {
                val name = context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                    if (it.moveToFirst()) it.getString(0) else null
                }?.takeIf { it.isNotBlank() } ?: "分享文件-${System.currentTimeMillis()}"
                val job = currentCoroutineContext()
                context.contentResolver.openInputStream(uri)?.use { input ->
                    UploadStaging.copy(root, name, input) { job.ensureActive() }
                } ?: error("无法读取文件，请通过来源应用重新分享")
            }
            val task = ShareTask(UUID.randomUUID().toString(), file.name, file.path, file.length(), owner, base, realm)
            lock.withLock { state.value += task; persist() }
            start(task.id)
        }
        messages.value = "已加入快传队列"
    }

    fun start(id: String) {
        if (jobs[id]?.isActive == true) return
        jobs[id] = scope.launch {
            var acquired = false
            try {
                uploads.acquire(); acquired = true
                var task = state.value.firstOrNull { it.id == id } ?: return@launch
                check(task.realm == AppConfig.getConfiguredBaseUrl() && task.owner == account()) { "请切回创建任务时的服务器和账号" }
                // 恢复任务时采用灾备核心已验证的当前入口，upload_id 仍属于同一后端会话。
                task = task.copy(base = AppConfig.getBaseUrl())
                check(task.result == null) { "分享已完成" }
                update(id) { it.copy(state = "上传中", error = "", base = task.base) }
                val token = Request.getToken() ?: error("请先登录")
                val api = ShareTransport(task.base, token, Request.deviceId())
                val link = api.upload(task, session = { session ->
                    update(id) { it.copy(uploadId = session) }
                }, progress = { sent ->
                    state.update { all -> all.map { if (it.id == id) it.copy(sent = sent) else it } }
                })
                check(link.share_code.isNotBlank()) { "服务端未返回分享链接" }
                update(id) { it.copy(state = "已完成", result = link, sent = task.size) }
                withContext(Dispatchers.IO) { UploadStaging.remove(root, task.path) }
                val copied = autoCopy && runCatching { copy(link.url(task.base)) }.isSuccess
                messages.value = if (copied) "${task.name} 上传完成，链接已复制" else "${task.name} 上传完成，可在队列中复制链接"
            } catch (e: CancellationException) {
                withContext(NonCancellable) { update(id) { if (it.result == null) it.copy(state = "已暂停") else it } }
                throw e
            } catch (e: Exception) {
                update(id) { it.copy(state = "失败", error = e.message ?: "上传失败") }
                messages.value = e.message ?: "上传失败"
            } finally {
                if (acquired) uploads.release()
            }
        }
    }

    fun pause(id: String) { jobs[id]?.cancel() }
    fun cancelAll() { jobs.values.toList().forEach { it.cancel() } }
    suspend fun remove(id: String) {
        jobs[id]?.cancelAndJoin()
        val task = state.value.firstOrNull { it.id == id } ?: return
        withContext(Dispatchers.IO) { UploadStaging.remove(root, task.path) }
        lock.withLock { state.value = state.value.filterNot { it.id == id }; persist() }
    }

    fun copy(text: String) {
        context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("FileSync 快传链接", text))
    }

    private suspend fun update(id: String, block: (ShareTask) -> ShareTask) {
        lock.withLock { state.update { all -> all.map { if (it.id == id) block(it) else it } }; persist() }
    }

    private suspend fun persist() = withContext(Dispatchers.IO) {
        queue.parentFile?.mkdirs()
        val temp = File(queue.parentFile, "queue.tmp")
        temp.writeText(Request.json.encodeToString(state.value))
        java.nio.file.Files.move(temp.toPath(), queue.toPath(), java.nio.file.StandardCopyOption.REPLACE_EXISTING,
            java.nio.file.StandardCopyOption.ATOMIC_MOVE)
    }
}
