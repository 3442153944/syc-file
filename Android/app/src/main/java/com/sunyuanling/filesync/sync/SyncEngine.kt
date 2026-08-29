// sync/SyncEngine.kt
// Android 同步引擎（对齐 SYNC_PROTOCOL.md，行为对标桌面端 ws_client/upload_worker/sync_engine）。
//
// 职责：
// 1. 执行服务端派发（WS file_sync/task_created）：download（.synctmp 原子发布）/delete/mkdir，
//    REST 回报 complete/failed；
// 2. 本地探测：RecursiveWatcher（inotify）→ 稳定窗防抖 → 分片上传（Rust 内核算描述）→
//    REST notify 上报 file_changed（带 base_hash 供 CAS）；
// 3. 连接追赶（每次 WS Connected）：拉取积压 pending 任务执行 + 逐 folder
//    「先上传本地离线变更（带 base 走 CAS，冲突安全）→ 再 scan_result 交给服务端比对补齐」，
//    避免离线重连后的流量爆发（scan 是元数据级 hash 比对，只传真正的差异）；
// 4. 冲突：收 conflict → 本地分叉隔离 .syncpending → 主目录收敛服务端版；
//    conflict_resolved(keep_local) → 以 server_hash 为 base 重新上传隔离副本。
//
// 限流：上传/下载各 Semaphore(2)；AppConfig.syncOnWifiOnly 时非 Wi-Fi 不做上传/追赶。
// 映射：同步文件夹由服务器（Windows 端创建）定义，本地目录映射是设备私有（SyncMappingStore）。
package com.sunyuanling.filesync.sync

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.util.Log
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsMessage
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.api.file.DownloadParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.api.sync.ScanItemDto
import com.sunyuanling.filesync.api.sync.SyncApi
import com.sunyuanling.filesync.api.sync.SyncFolderInfo
import com.sunyuanling.filesync.api.sync.SyncNotifyParams
import com.sunyuanling.filesync.api.sync.SyncScanParams
import com.sunyuanling.filesync.core.FileCore
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.util.Blake3Util
import com.sunyuanling.filesync.util.DeviceInfoUtil
import com.sunyuanling.filesync.util.FileLogger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Request as OkRequest
import java.io.File
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

// ---- WS 事件 DTO（对齐 internal/sync/types.go TaskNotify 与 SYNC_PROTOCOL §3.2）----

@Serializable
private data class TaskCreatedDto(
    val event: String = "",
    @SerialName("task_id") val taskId: Long = 0,
    @SerialName("task_type") val taskType: String = "",
    @SerialName("folder_id") val folderId: Long = 0,
    @SerialName("relative_path") val relativePath: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_hash") val fileHash: String = "",
    @SerialName("remote_path") val remotePath: String = "",
    @SerialName("remote_dir") val remoteDir: String = "",
)

@Serializable
private data class ConflictDto(
    @SerialName("conflict_id") val conflictId: Long = 0,
    @SerialName("folder_id") val folderId: Long = 0,
    @SerialName("relative_path") val relativePath: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("server_hash") val serverHash: String = "",
    @SerialName("server_version") val serverVersion: Long = 0,
    @SerialName("base_hash") val baseHash: String = "",
    @SerialName("local_hash") val localHash: String = "",
)

@Serializable
private data class ConflictResolvedDto(
    @SerialName("conflict_id") val conflictId: Long = 0,
    val resolution: String = "",
    @SerialName("server_hash") val serverHash: String = "",
)

object SyncEngine {

    private const val TAG = "SyncEngine"
    private const val STABLE_DEBOUNCE_MS = 2_000L
    private const val STABLE_RECHECK_MS = 400L
    private const val UPLOAD_CONCURRENCY = 2
    private const val DOWNLOAD_CONCURRENCY = 2

    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    private var scope: CoroutineScope? = null
    private var appContext: Context? = null
    private var deviceId: String = ""

    private val _running = MutableStateFlow(false)
    val running: StateFlow<Boolean> = _running.asStateFlow()

    private val _lastActivity = MutableStateFlow("未启动")
    val lastActivity: StateFlow<String> = _lastActivity.asStateFlow()

    private val folders = MutableStateFlow<Map<Long, SyncFolderInfo>>(emptyMap())
    private val _folder = MutableStateFlow<SyncFolderInfo?>(null)
    /** 该账号唯一的同步文件夹配置（UI 消费用），未配置为 null。 */
    val folder: StateFlow<SyncFolderInfo?> = _folder.asStateFlow()
    private val watchers = ConcurrentHashMap<Long, RecursiveWatcher>()
    private val debounceJobs = ConcurrentHashMap<String, Job>()
    private val inFlightTasks: MutableSet<Long> = ConcurrentHashMap.newKeySet()
    private val uploadSem = Semaphore(UPLOAD_CONCURRENCY)
    private val downloadSem = Semaphore(DOWNLOAD_CONCURRENCY)
    private val catchUpMutex = Mutex()

    // ------------------------------------------------------------------ 生命周期

    /** 幂等启动。autoSyncEnabled 的消费者；由 FileSyncApp / SyncKeepAliveService 调用。 */
    @Synchronized
    fun start(context: Context) {
        if (_running.value) return
        val ctx = context.applicationContext
        appContext = ctx
        deviceId = DeviceInfoUtil.getDeviceId(ctx)

        val s = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        scope = s

        // WS 入站事件（SharedFlow，不丢消息）
        s.launch {
            WebSocketManager.events.collect { msg ->
                if (msg is WsMessage.Text) handleWsText(msg.content)
            }
        }
        // 每次连上（含首次）做一轮追赶
        s.launch {
            WebSocketManager.connectionState.collect { st ->
                if (st is WsState.Connected) {
                    runCatching { catchUp() }
                        .onFailure { log("追赶失败: ${it.message}") }
                }
            }
        }
        _running.value = true
        log("同步引擎已启动 (device=$deviceId)")
    }

    @Synchronized
    fun stop() {
        watchers.values.forEach { it.stop() }
        watchers.clear()
        debounceJobs.clear()
        scope?.cancel()
        scope = null
        SyncBaseStore.flush()
        _running.value = false
        log("同步引擎已停止")
    }

    /** 映射变更（配置页保存后调用）：清除旧基线、重建 watcher 并触发一轮追赶。 */
    fun onMappingsChanged(clearedFolderId: Long? = null) {
        if (clearedFolderId != null) {
            SyncBaseStore.clearFolder(clearedFolderId)
            log("已清除 folder=$clearedFolderId 的旧基线")
        }
        triggerCatchUp()
    }

    /** 手动触发一轮追赶（执行积压任务 + 先传本地变更再 scan 比对）。待处理页「重新对齐」用。 */
    fun triggerCatchUp() {
        val s = scope ?: return
        s.launch {
            runCatching { catchUp() }.onFailure { log("追赶失败: ${it.message}") }
        }
    }

    // ------------------------------------------------------------------ WS 入站

    private suspend fun handleWsText(text: String) {
        val content: JsonObject = runCatching {
            val root = json.parseToJsonElement(text).let { it as? JsonObject } ?: return
            if (root["type"]?.jsonPrimitive?.content != "file_sync") return
            root["content"] as? JsonObject ?: return
        }.getOrNull() ?: return

        when (content["event"]?.jsonPrimitive?.content) {
            "task_created" -> runCatching {
                json.decodeFromJsonElement(TaskCreatedDto.serializer(), content)
            }.getOrNull()?.let { dispatchTask(it) }

            "conflict" -> runCatching {
                json.decodeFromJsonElement(ConflictDto.serializer(), content)
            }.getOrNull()?.let { handleConflict(it) }

            "conflict_resolved" -> runCatching {
                json.decodeFromJsonElement(ConflictResolvedDto.serializer(), content)
            }.getOrNull()?.let { handleConflictResolved(it) }
        }
    }

    // ------------------------------------------------------------------ 任务执行

    private fun dispatchTask(t: TaskCreatedDto) {
        if (t.taskId <= 0 || !inFlightTasks.add(t.taskId)) return // 去重（WS + pending 拉取可能重叠）
        scope?.launch {
            try {
                execTask(t)
            } finally {
                inFlightTasks.remove(t.taskId)
            }
        }
    }

    private suspend fun execTask(t: TaskCreatedDto) {
        val mapping = SyncMappingStore.enabledMappingFor(t.folderId)
        if (mapping == null) {
            SyncApi.failTask(t.taskId, "本设备未映射该同步文件夹")
            return
        }
        val localRoot = File(mapping.localPath)
        val rel = sanitizeRel(t.relativePath)
        if (rel == null) {
            SyncApi.failTask(t.taskId, "非法相对路径")
            return
        }

        when (t.taskType) {
            // 基线/静音键统一用消毒后的 rel（与 watcher 事件推导的 fs 形式一致），
            // 绝不能用 t.relativePath 原文——形式不一致会导致基线查不到，
            // 下载下来的文件被 watcher 当新文件回传，设备间打乒乓。
            "download" -> downloadSem.withPermit {
                log("下载 $rel")
                downloadAndPublish(t.folderId, localRoot, rel, t.fileName, t.remoteDir, t.fileHash.ifEmpty { null })
                    .onSuccess { hash ->
                        val f = File(localRoot, rel)
                        SyncBaseStore.set(t.folderId, rel, hash, f.length(), f.lastModified() / 1000)
                        SyncApi.completeTask(t.taskId, hash)
                        log("已下载并发布 $rel")
                    }
                    .onFailure { e ->
                        SyncApi.failTask(t.taskId, e.message ?: "下载失败")
                        log("下载失败 $rel: ${e.message}")
                    }
            }

            "delete" -> {
                val f = File(localRoot, rel)
                muteWatch(t.folderId, rel)
                f.delete() // 不存在也视为成功
                SyncBaseStore.remove(t.folderId, rel)
                SyncApi.completeTask(t.taskId)
                log("已删除本地 $rel")
            }

            "mkdir" -> {
                val ok = File(localRoot, rel).let { it.mkdirs() || it.isDirectory }
                if (ok) SyncApi.completeTask(t.taskId) else SyncApi.failTask(t.taskId, "创建目录失败")
            }
        }
    }

    /** 下载 → 流式 blake3 校验 → 写 .synctmp → 原子 rename 发布。返回落盘 hash。 */
    private suspend fun downloadAndPublish(
        folderId: Long,
        localRoot: File,
        rel: String,
        fileName: String,
        remoteDir: String,
        expectedHash: String?,
    ): Result<String> {
        val url = FileApi.buildDownloadUrl(DownloadParams(path = remoteDir, name = fileName))
        val tmpDir = File(localRoot, ".synctmp").apply { mkdirs() }
        val tmp = File(tmpDir, "$fileName.${UUID.randomUUID()}.tmp")
        try {
            val hasher = Blake3Util.newHasher()
            val contentLength = Request.client.newCall(OkRequest.Builder().url(url).get().build()).execute().use { resp ->
                if (!resp.isSuccessful) return Result.failure(Exception("HTTP ${resp.code} url=$url"))
                val body = resp.body ?: return Result.failure(Exception("响应体为空 url=$url"))
                var total = 0L
                tmp.outputStream().use { out ->
                    body.byteStream().use { input ->
                        val buf = ByteArray(256 * 1024)
                        while (true) {
                            val n = input.read(buf)
                            if (n < 0) break
                            out.write(buf, 0, n)
                            hasher.update(if (n == buf.size) buf else buf.copyOf(n))
                            total += n
                        }
                    }
                }
                total
            }
            val actual = Blake3Util.toHex(hasher.digest())
            log("下载校验 url=$url actualHash=$actual size=$contentLength expectedHash=$expectedHash")
            if (!expectedHash.isNullOrEmpty() && !expectedHash.equals(actual, ignoreCase = true)) {
                return Result.failure(Exception("hash 不匹配 expected=$expectedHash actual=$actual"))
            }

            val final = File(localRoot, rel)
            final.parentFile?.mkdirs()
            // 发布产生的本地事件不能再被当作变更上报回环
            muteWatch(folderId, rel)
            if (!tmp.renameTo(final)) {
                tmp.copyTo(final, overwrite = true)
                tmp.delete()
            }
            return Result.success(actual)
        } catch (e: Exception) {
            return Result.failure(e)
        } finally {
            if (tmp.exists()) tmp.delete()
        }
    }

    // ------------------------------------------------------------------ 追赶（每次连上执行）

    private suspend fun catchUp() = catchUpMutex.withLock {
        if (!Request.hasToken()) return@withLock
        SyncMappingStore.load()
        SyncBaseStore.load()
        refreshFolders()
        rebuildWatchers()

        // 1) 积压任务：离线期间服务端存起来的 pending（Reaper 也会重派，这里主动拉一把）
        SyncApi.pendingTasks(deviceId).getOrNull()?.data.orEmpty().forEach { task ->
            dispatchTask(
                TaskCreatedDto(
                    event = "task_created", taskId = task.id, taskType = task.taskType,
                    folderId = task.folderId, relativePath = task.relativePath,
                    fileName = task.fileName, fileSize = task.fileSize,
                    fileHash = task.fileHash ?: "",
                    remoteDir = remoteDirFor(task.folderId, task.relativePath) ?: "",
                )
            )
        }

        // 2) 逐 folder：先推本地离线变更（CAS 冲突安全），再 scan 交服务端比对补齐
        if (!networkAllowed()) {
            log("非 Wi-Fi 网络，跳过追赶上传/扫描")
            return@withLock
        }
        for ((folderId, folder) in folders.value) {
            val mapping = SyncMappingStore.enabledMappingFor(folderId) ?: continue
            if (!folder.enabled) continue
            runCatching { catchUpFolder(folder, File(mapping.localPath)) }
                .onFailure { log("folder ${folder.name} 追赶失败: ${it.message}") }
        }
        log("追赶完成")
    }

    private suspend fun catchUpFolder(folder: SyncFolderInfo, localRoot: File) {
        if (!localRoot.exists()) localRoot.mkdirs()
        val files = ArrayList<Pair<String, File>>() // rel → file
        val dirs = ArrayList<String>()
        walk(localRoot, "", files, dirs)

        // Phase 1：本地离线变更先上传（download_only 的 folder 不上传）
        if (folder.direction != "download_only") {
            // coroutineScope + launch：追赶一批本地变更时不能挨个 await 网络往返——
            // 大批量删除/新建时会被拖到几百次 HTTP 往返串行执行，实测能拖到几分钟。
            // uploadAndNotify 内部本来就有 uploadSem 限流，这里并发发起才能真正吃到那个限流，
            // 而不是靠外层顺序调用把并发数锁死在 1。
            coroutineScope {
                for ((rel, f) in files) {
                    val base = SyncBaseStore.get(folder.id, rel)
                    val size = f.length()
                    val mtime = f.lastModified() / 1000
                    when {
                        base == null -> launch { uploadAndNotify(folder, localRoot, rel, f, action = "create", baseHash = "") }
                        base.size != size || base.mtime != mtime -> launch {
                            val hash = computeFileHash(f)
                            if (hash != null && hash != base.hash) {
                                uploadAndNotify(folder, localRoot, rel, f, action = "modify", baseHash = base.hash)
                            } else if (hash != null) {
                                SyncBaseStore.set(folder.id, rel, base.hash, size, mtime) // 只刷新 stat
                            }
                        }
                    }
                }
                // 本地已删（有基线但文件不在）→ 上报 delete
                val present = files.map { it.first }.toHashSet()
                for ((rel, base) in SyncBaseStore.folderSnapshot(folder.id)) {
                    if (rel !in present) {
                        launch {
                            SyncApi.notify(
                                SyncNotifyParams(
                                    deviceId = deviceId, folderId = folder.id,
                                    relativePath = rel, fileName = rel.substringAfterLast('/'),
                                    action = "delete", baseHash = base.hash,
                                )
                            ).onSuccess { SyncBaseStore.remove(folder.id, rel) }
                        }
                    }
                }
            }
        }

        // Phase 2：全量清单交服务端比对（trunk 有本地无→download；trunk 无本地有→delete）
        val items = ArrayList<ScanItemDto>(files.size + dirs.size)
        dirs.mapTo(items) { rel ->
            ScanItemDto(relativePath = rel, fileName = rel.substringAfterLast('/'), isDir = true)
        }
        for ((rel, f) in files) {
            val hash = SyncBaseStore.get(folder.id, rel)?.hash ?: computeFileHash(f) ?: continue
            items.add(
                ScanItemDto(
                    relativePath = rel, fileName = f.name, fileSize = f.length(),
                    fileHash = hash, mtime = f.lastModified() / 1000,
                )
            )
        }
        SyncApi.scan(SyncScanParams(deviceId = deviceId, folderId = folder.id, items = items))
    }

    private fun walk(dir: File, relPrefix: String, files: MutableList<Pair<String, File>>, dirs: MutableList<String>) {
        dir.listFiles()?.forEach { f ->
            if (shouldIgnore(f.name)) return@forEach
            val rel = if (relPrefix.isEmpty()) f.name else "$relPrefix/${f.name}"
            if (f.isDirectory) {
                dirs.add(rel)
                walk(f, rel, files, dirs)
            } else if (f.isFile) {
                files.add(rel to f)
            }
        }
    }

    // ------------------------------------------------------------------ 探测（watcher）

    private suspend fun refreshFolders() {
        // 系统始终只保留一个同步文件夹；内部仍以 Map 存取（0/1 个元素），
        // 保留既有的按 folderId 查找逻辑不动。
        SyncApi.getFolder().getOrNull()?.let { resp ->
            folders.value = resp.data?.let { mapOf(it.id to it) } ?: emptyMap()
            _folder.value = resp.data
        }
    }

    private fun rebuildWatchers() {
        watchers.values.forEach { it.stop() }
        watchers.clear()
        for ((folderId, folder) in folders.value) {
            if (!folder.enabled) continue
            val mapping = SyncMappingStore.enabledMappingFor(folderId) ?: continue
            if (folder.direction == "download_only") continue // 只下不传，无需探测
            val root = File(mapping.localPath)
            if (!root.exists()) root.mkdirs()
            val watcher = RecursiveWatcher(root, ::shouldIgnore) { file, event ->
                onWatchEvent(folderId, root, file, event)
            }
            watcher.start()
            watchers[folderId] = watcher
        }
        log("监听中 folder=${watchers.keys}")
    }

    /** 近期由引擎自己写入/删除的路径（发布/执行任务），忽略其触发的 watcher 事件防回环。 */
    private val selfMuted = ConcurrentHashMap<String, Long>()

    private fun muteWatch(folderId: Long, rel: String) {
        selfMuted["$folderId|$rel"] = System.currentTimeMillis()
    }

    private fun isSelfMuted(folderId: Long, rel: String): Boolean {
        val t = selfMuted["$folderId|$rel"] ?: return false
        if (System.currentTimeMillis() - t > 10_000) {
            selfMuted.remove("$folderId|$rel")
            return false
        }
        return true
    }

    private fun onWatchEvent(folderId: Long, root: File, file: File, event: Int) {
        val rel = file.absolutePath.removePrefix(root.absolutePath)
            .trimStart('/', '\\').replace('\\', '/')
        if (rel.isEmpty() || rel.split('/').any { shouldIgnore(it) }) return
        if (isSelfMuted(folderId, rel)) return

        val s = scope ?: return
        val key = "$folderId|$rel"
        // 防抖：稳定 2s 后再处理（Office 类保存会有多次写入/换名）
        debounceJobs[key]?.cancel()
        debounceJobs[key] = s.launch {
            delay(STABLE_DEBOUNCE_MS)
            debounceJobs.remove(key)
            handleStableChange(folderId, root, rel, file)
        }
    }

    private suspend fun handleStableChange(folderId: Long, root: File, rel: String, file: File) {
        val folder = folders.value[folderId] ?: return
        if (!file.exists()) {
            // 删除：只对有基线的（同步过的）上报
            val base = SyncBaseStore.get(folderId, rel) ?: return
            log("检测到删除 $rel")
            SyncApi.notify(
                SyncNotifyParams(
                    deviceId = deviceId, folderId = folderId,
                    relativePath = rel, fileName = rel.substringAfterLast('/'),
                    action = "delete", baseHash = base.hash,
                )
            ).onSuccess { SyncBaseStore.remove(folderId, rel) }
            return
        }
        if (file.isDirectory) {
            // 新目录：上报 mkdir 派发到其它设备
            SyncApi.notify(
                SyncNotifyParams(
                    deviceId = deviceId, folderId = folderId,
                    relativePath = rel, fileName = file.name,
                    action = "create", isDir = true,
                )
            )
            return
        }
        // 稳定窗二次确认：size/mtime 复查，仍在变就重新防抖
        val size1 = file.length(); val mtime1 = file.lastModified()
        delay(STABLE_RECHECK_MS)
        if (file.length() != size1 || file.lastModified() != mtime1) {
            onWatchEvent(folderId, root, file, 0)
            return
        }
        if (!networkAllowed()) {
            log("非 Wi-Fi，暂不上传 $rel（下次追赶补传）")
            return
        }
        val base = SyncBaseStore.get(folderId, rel)
        // 内容未变（例如只是 touch）：hash 相等则只刷新 stat
        if (base != null) {
            val hash = computeFileHash(file)
            if (hash == base.hash) {
                SyncBaseStore.set(folderId, rel, base.hash, file.length(), file.lastModified() / 1000)
                return
            }
        }
        uploadAndNotify(
            folder, root, rel, file,
            action = if (base == null) "create" else "modify",
            baseHash = base?.hash ?: "",
        )
    }

    // ------------------------------------------------------------------ 上传 + 上报

    private suspend fun uploadAndNotify(
        folder: SyncFolderInfo,
        localRoot: File,
        rel: String,
        file: File,
        action: String,
        baseHash: String,
    ) {
        uploadSem.withPermit {
            val remoteDir = joinRemote(folder.remotePath, rel.substringBeforeLast('/', ""))
            log("上传 $rel")
            // 不再先删远端旧文件：同步场景下 base 丢失/被清（比如一次映射重设）会让一批
            // 其实早就同步过的文件被误判成"本地新文件"，这里如果照旧先删除服务端正确内容
            // 再重传，一旦重传失败就是真实数据丢失。服务端 init 本来就会处理"内容相同"
            // 的情况（同路径+同大小+同哈希直接秒传成功，不需要真的删除重传）；真冲突
            // （同名不同内容）会被拒绝，交给上层冲突协议处理，好过静默删掉对方的数据。
            val result = ChunkedUploader.upload(
                file, remoteDir,
                ChunkedUploader.UploadOptions(deviceId = deviceId),
            )
            result.onSuccess { complete ->
                SyncApi.notify(
                    SyncNotifyParams(
                        deviceId = deviceId, folderId = folder.id,
                        relativePath = rel, fileName = file.name, action = action,
                        fileSize = complete.fileSize, fileHash = complete.fileHash,
                        baseHash = baseHash, mtime = file.lastModified() / 1000,
                    )
                ).onSuccess {
                    // 被接受则 trunk 即本内容；若实际冲突，conflict 事件会再纠正基线。
                    // notify 失败则不落基线，留给下次追赶重报。
                    SyncBaseStore.set(folder.id, rel, complete.fileHash, file.length(), file.lastModified() / 1000)
                    log("已上传并上报 $rel")
                }.onFailure { e ->
                    log("上报失败 $rel（基线未更新，追赶时重报）: ${e.message}")
                }
            }.onFailure { e ->
                log("上传失败 $rel: ${e.message}")
            }
        }
    }

    // ------------------------------------------------------------------ 冲突

    private suspend fun handleConflict(cf: ConflictDto) {
        val mapping = SyncMappingStore.enabledMappingFor(cf.folderId) ?: return
        val folder = folders.value[cf.folderId] ?: return
        val localRoot = File(mapping.localPath)
        val rel = sanitizeRel(cf.relativePath) ?: return
        val main = File(localRoot, rel)

        // 1) 本地分叉隔离到 .syncpending/<conflictId>_<name>（resolution 时可定位）
        val pendDir = File(localRoot, ".syncpending").apply { mkdirs() }
        val quarantine = File(pendDir, "${cf.conflictId}_${cf.fileName}")
        if (main.exists()) {
            muteWatch(cf.folderId, rel)
            if (!main.renameTo(quarantine)) {
                runCatching { main.copyTo(quarantine, overwrite = true); main.delete() }
            }
        }
        log("冲突 #${cf.conflictId} $rel：本地版已隔离，收敛服务端版本")

        // 2) 主目录收敛服务端版本
        val remoteDir = joinRemote(folder.remotePath, rel.substringBeforeLast('/', ""))
        downloadSem.withPermit {
            downloadAndPublish(cf.folderId, localRoot, rel, cf.fileName, remoteDir, cf.serverHash.ifEmpty { null })
                .onSuccess { hash ->
                    SyncBaseStore.set(cf.folderId, rel, hash, main.length(), main.lastModified() / 1000)
                }
                .onFailure { log("冲突收敛下载失败 $rel: ${it.message}") }
        }
    }

    private suspend fun handleConflictResolved(cr: ConflictResolvedDto) {
        // 找到当初隔离的副本
        val hit = folders.value.keys.asSequence()
            .mapNotNull { fid ->
                val mapping = SyncMappingStore.enabledMappingFor(fid) ?: return@mapNotNull null
                val pend = File(mapping.localPath, ".syncpending")
                pend.listFiles()?.firstOrNull { it.name.startsWith("${cr.conflictId}_") }
                    ?.let { Triple(fid, mapping, it) }
            }
            .firstOrNull() ?: return
        val (folderId, mapping, quarantine) = hit
        val folder = folders.value[folderId] ?: return

        when (cr.resolution) {
            "accept_server" -> {
                quarantine.delete()
                log("冲突 #${cr.conflictId} 采用服务器版，已丢弃本地副本")
            }
            "keep_local" -> {
                val origName = quarantine.name.removePrefix("${cr.conflictId}_")
                // 副本以原名放回主目录（覆盖服务端版），再以 server_hash 为 base 上报——快进
                val localRoot = File(mapping.localPath)
                val rel = origName // 隔离时未保留子目录信息，冲突文件按根目录名回放；子目录场景由重扫兜底
                val main = File(localRoot, rel)
                muteWatch(folderId, rel)
                runCatching { quarantine.copyTo(main, overwrite = true); quarantine.delete() }
                uploadAndNotify(folder, localRoot, rel, main, action = "modify", baseHash = cr.serverHash)
                log("冲突 #${cr.conflictId} 保留本地版，已重新上传")
            }
        }
    }

    // ------------------------------------------------------------------ 工具

    private fun remoteDirFor(folderId: Long, relativePath: String): String? {
        val folder = folders.value[folderId] ?: return null
        return joinRemote(folder.remotePath, relativePath.replace('\\', '/').substringBeforeLast('/', ""))
    }

    /** 远端目录拼接：远端根（Windows 路径）+ 相对子目录（'/'）。 */
    private fun joinRemote(remoteRoot: String, relDir: String): String {
        val root = remoteRoot.trimEnd('/', '\\')
        return if (relDir.isEmpty()) root else "$root/$relDir"
    }

    /** 相对路径消毒：统一 '/'、去空段、拒绝 '..'。 */
    private fun sanitizeRel(rel: String): String? {
        val parts = rel.replace('\\', '/').split('/').filter { it.isNotEmpty() && it != "." }
        if (parts.isEmpty() || parts.any { it == ".." }) return null
        return parts.joinToString("/")
    }

    /** 忽略名单：同步内部目录、Office 锁文件、常见临时文件。 */
    private fun shouldIgnore(name: String): Boolean =
        name == ".synctmp" || name == ".syncpending" ||
                name.startsWith("~$") || name.endsWith(".tmp") || name.endsWith(".part") ||
                name.endsWith(".crdownload") || name == ".DS_Store" || name == "Thumbs.db"

    /** Wi-Fi 门控：syncOnWifiOnly 时仅 Wi-Fi/以太网可上传与追赶。 */
    private fun networkAllowed(): Boolean {
        if (!AppConfig.syncOnWifiOnly) return true
        val cm = appContext?.getSystemService(ConnectivityManager::class.java) ?: return false
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        return caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) ||
                caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)
    }

    private fun computeFileHash(file: File): String? {
        FileCore.fileHashHex(file)?.let { return it }
        // 纯 Java 回退：流式整文件 blake3
        return runCatching {
            val hasher = Blake3Util.newHasher()
            file.inputStream().use { input ->
                val buf = ByteArray(256 * 1024)
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    hasher.update(if (n == buf.size) buf else buf.copyOf(n))
                }
            }
            Blake3Util.toHex(hasher.digest())
        }.getOrNull()
    }

    private fun log(msg: String) {
        _lastActivity.value = msg
        Log.i(TAG, msg)
        runCatching { FileLogger.i(TAG, msg) }
    }
}
