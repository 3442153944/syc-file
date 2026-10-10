// api/file/ChunkedUploader.kt
// 分片上传编排：算描述信息（叶子/树根/整文件哈希）→ init →（秒传则完成）→ 乱序并发补传缺失分片 → complete。
//
// 两条路径：
// - 自适应（默认，chunkSize=null）：先跑 sync_core upload_planner 的探测循环（每节点微样本
//   测 RTT + 吞吐样本测带宽，10min 内有新鲜持久化样本则跳过），按总带宽选分片档位；传输期由
//   planner 做多路径调度（父链折算共享瓶颈、per-node AIMD 窗口、动态超时、重排重试
//   CHUNK_ATTEMPTS=3 全部由 planner 负责，本端不做分片级内部重试）。Chunk 任务带明确
//   node url，必须打到该 URL（多路径必须落在同一会话上，由 planner 做同后端校验）。
// - 固定覆盖（chunkSize 显式值）：跳过探测，init/chunk 全走激活节点的旧单路径行为，可预期。
//
// 设计要点：
// - 进度：字节级，基于已成功分片累计字节数回调；init 阶段先置「已落盘分片」估算基线。
// - 取消：本函数为 suspend，调用方所在协程被 cancel 即停止派发新分片；已在途的 chunk 请求随 IO 抛
//   CancellationException。每个分片任务开头 ensureActive()，确保 cancel 后立即响应。
// - 会话过期：planner 路径分片收 404 → 回报 ChunkSessionGone → planner 发 Task::Reinit → 同一
//   Description 重新 init（不重算哈希）→ planner.resume 续传；固定路径 404 → SessionGoneException，
//   由外层 upload() 捕获后自动重新 init 整流程一次。
package com.sunyuanling.filesync.api.file

import android.os.SystemClock
import android.util.Log
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.core.SyncCore
import com.sunyuanling.filesync.core.SyncPlanner
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.util.Blake3Util
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Channel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request as OkRequest
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.io.RandomAccessFile
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong

/** 分片校验失败：服务端拒收该片，需重传。 */
class ChunkVerifyException(val index: Int, message: String) : Exception(message)

/** 会话不存在（过期/被清）：需重新 init 整个上传。 */
class SessionGoneException(message: String) : Exception(message)

object ChunkedUploader {

    private const val TAG = "ChunkedUploader"

    /** 固定路径的默认分片 4MiB（自适应路径由 planner 探测选档）。 */
    const val DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024

    /** 固定路径的默认并发分片数（自适应路径用 planner 的 per-node AIMD 窗口）。 */
    const val DEFAULT_CONCURRENCY = 3

    /** 测速接口路由（服务端 POST /v1/net/speedtest/upload?bytes=N）。 */
    private const val SPEEDTEST_PATH = "/v1/net/speedtest/upload"

    /** 传输循环停滞保护：planner 单片动态超时上限 120s，2 倍仍无分片回报说明调度/执行挂死。 */
    private const val STALL_TIMEOUT_MS = 240_000L

    /** 窗口调整冷却，对齐 sync_core 的 ADJUST_COOLDOWN_MS（防振荡）。 */
    private const val PLANNER_ADJUST_COOLDOWN_MS = 2_000L

    data class UploadOptions(
        /** 分片大小：null=自适应（planner 探测选档 + 多路径调度，默认）；
         *  显式值=固定分片，走激活节点的旧单路径行为。 */
        val chunkSize: Int? = null,
        /** 并发覆盖：null=planner 的 per-node AIMD 窗口；显式值=固定路径的 Semaphore 并发数。 */
        val concurrency: Int? = null,
        /** 本机设备 id，服务端派发同步任务时排除源设备。空字符串表示不传。 */
        val deviceId: String = ""
    )

    data class Description(
        val totalSize: Long,
        val chunkSize: Int,
        val chunkCount: Int,
        val leafHashesHex: List<String>,
        val merkleRootHex: String,
        val fileHashHex: String
    )

    /**
     * 计算上传描述：优先走 Rust 原生内核（SyncCore/JNI，mmap+rayon 并行，
     * 与服务端 fc_finalize 逐字节一致）；原生不可用时回退纯 Java 单趟流式。
     */
    fun describe(file: File, chunkSize: Int = DEFAULT_CHUNK_SIZE): Description {
        SyncCore.describe(file, chunkSize)?.let { return it }
        return describeJava(file, chunkSize)
    }

    /** 纯 Java 回退：一趟顺序读，算每片 blake3 叶子 + 整文件流式 blake3 + Merkle 树根。 */
    private fun describeJava(file: File, chunkSize: Int): Description {
        val total = file.length()
        val count = if (total == 0L) 1 else ((total + chunkSize - 1) / chunkSize).toInt()
        val leaves = ArrayList<ByteArray>(count)
        val leafHex = ArrayList<String>(count)
        val fileHasher = Blake3Util.newHasher()

        file.inputStream().buffered().use { input ->
            val buf = ByteArray(chunkSize)
            while (true) {
                var filled = 0
                while (filled < buf.size) {
                    val n = input.read(buf, filled, buf.size - filled)
                    if (n < 0) break
                    filled += n
                }
                if (filled == 0) break
                val block = if (filled == buf.size) buf else buf.copyOf(filled)
                fileHasher.update(block)
                val leaf = Blake3Util.hash(block)
                leaves.add(leaf)
                leafHex.add(Blake3Util.toHex(leaf))
                if (filled < buf.size) break // 末片
            }
        }

        val root = Blake3Util.merkleRoot(leaves)
        val fileHash = fileHasher.digest()
        return Description(
            totalSize = total,
            chunkSize = chunkSize,
            chunkCount = leaves.size,
            leafHashesHex = leafHex,
            merkleRootHex = Blake3Util.toHex(root),
            fileHashHex = Blake3Util.toHex(fileHash)
        )
    }

    /**
     * 上传整份文件。
     * @param onProgress (已成功字节, 总字节)
     * @return 成功时的完成数据（秒传也算成功）
     */
    suspend fun upload(
        file: File,
        remoteDir: String,
        options: UploadOptions = UploadOptions(),
        onProgress: (bytesSent: Long, totalBytes: Long) -> Unit = { _, _ -> }
    ): Result<UploadCompleteData> = withContext(Dispatchers.IO) {
        try {
            val chunkOverride = options.chunkSize
            val data = if (chunkOverride != null) {
                // 固定覆盖路径：describe 一次，SessionGone 重 init 一次（都不重算哈希）
                val desc = describe(file, chunkOverride)
                onProgress(0, desc.totalSize)
                runFixedWithRetry(desc, file, remoteDir, options, onProgress)
            } else {
                // 自适应路径（默认）：planner 探测选档 + 多路径调度
                runPlanned(file, remoteDir, options, onProgress)
            }
            Result.success(data)
        } catch (ce: CancellationException) {
            // 取消向上传播，绝不能被通用 catch 吞掉
            throw ce
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /** 固定路径：一次完整的「init → 乱序并发补传 → complete」，会话过期重 init 一次。 */
    private suspend fun runFixedWithRetry(
        desc: Description,
        file: File,
        remoteDir: String,
        options: UploadOptions,
        onProgress: (Long, Long) -> Unit
    ): UploadCompleteData = try {
        runUploadOnce(desc, file, remoteDir, options, onProgress)
    } catch (e: SessionGoneException) {
        // 会话过期：重新 init 整流程一次（不会重算哈希）
        runUploadOnce(desc, file, remoteDir, options, onProgress)
    }

    /**
     * 一次完整的「init → 乱序并发补传 → complete」流程（固定路径）。
     * 会话过期时抛 SessionGoneException 由 runFixedWithRetry 重试。
     */
    private suspend fun runUploadOnce(
        desc: Description,
        file: File,
        remoteDir: String,
        options: UploadOptions,
        onProgress: (Long, Long) -> Unit
    ): UploadCompleteData = coroutineScope {
        val init = callInit(desc, file.name, remoteDir, options)

        // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
        // 不能调 complete（会 404 会话不存在），结果就地合成。
        if (init.instant) {
            onProgress(desc.totalSize, desc.totalSize)
            return@coroutineScope UploadCompleteData(
                fileName = file.name,
                storagePath = remoteDir,
                fileSize = desc.totalSize,
                fileHash = desc.fileHashHex,
                synced = true,
            )
        }

        val missing = if (init.missing.isNotEmpty()) init.missing else (0 until desc.chunkCount).toList()
        // 已落盘分片估算基线（用片数×chunkSize 上界，complete 时会自然修正）
        val bytesSent = AtomicLong((desc.chunkCount - missing.size).toLong() * desc.chunkSize)
        onProgress(minOf(bytesSent.get(), desc.totalSize), desc.totalSize)

        val sem = Semaphore((options.concurrency ?: DEFAULT_CONCURRENCY).coerceAtLeast(1))
        // 任一分片失败（含 SessionGone）→ coroutineScope 立即取消兄弟并向上抛
        missing.map { index ->
            async {
                ensureActive()
                sem.withPermit {
                    ensureActive()
                    val len = readAndUploadChunk(file, init.uploadId, index, desc)
                    val now = minOf(bytesSent.addAndGet(len.toLong()), desc.totalSize)
                    onProgress(now, desc.totalSize)
                }
            }
        }.awaitAll()

        completeUpload(init.uploadId, options.deviceId)
    }

    /** 读出该片字节并上传（单次尝试：失败抛给上层——planner 路径才是重试机制；
     *  固定路径的重试语义 = 外层 SessionGone 重 init 一次 + 上层 SyncEngine 重试）。 */
    private suspend fun readAndUploadChunk(
        file: File, uploadId: String, index: Int, desc: Description
    ): Int {
        val offset = index.toLong() * desc.chunkSize
        val len = minOf(desc.chunkSize.toLong(), desc.totalSize - offset).toInt()
        val data = ByteArray(len)
        RandomAccessFile(file, "r").use { raf ->
            raf.seek(offset)
            raf.readFully(data)
        }
        FileApi.uploadChunk(uploadId, index, data).getOrElse { throw it }
        return len
    }

    // ---------------------------------------------------------------- 多路径自适应（planner）路径
    //
    // planner 是决策核心（sync_core upload_planner），HTTP 由本侧执行：循环
    // next_task → 执行 → report() 直到 Done/Failed。探测 inline；分片每片一个协程，
    // 结果经 Channel 喂回循环；Task::Wait = 无任务可分派，挂在 Channel 上等在途分片的 Report。

    /** planner 节点配置（对齐 sync_core PlannerNodeConfig）。 */
    private data class PlannerNode(val id: String, val url: String, val parent: String? = null)

    /** planner 任务（Task JSON 的解析结果，变体与 upload_planner::Task 一一对应）。 */
    private sealed interface PlannerTask {
        data class Probe(val node: String, val url: String, val kind: ProbeKind, val bytes: Long) : PlannerTask
        data class Chunk(
            val seq: Long, val node: String, val url: String,
            val index: Int, val timeoutMs: Long, val bytes: Long
        ) : PlannerTask
        data object Reinit : PlannerTask
        data object Wait : PlannerTask
        data object Done : PlannerTask
        data class Failed(val reason: String) : PlannerTask
    }

    private enum class ProbeKind { TINY, THROUGHPUT }

    /** 在途分片的执行结果：正常 Report 喂回 planner；Fatal 是平台级失败（读文件错误等），
     *  没法也不应交给 planner 重排，整趟上传直接失败。 */
    private sealed interface ChunkOutcome {
        data class Report(val json: JsonObject) : ChunkOutcome
        data class Fatal(val message: String) : ChunkOutcome
    }

    private val plannerJson = Json { ignoreUnknownKeys = true; isLenient = true }

    /**
     * 自适应路径：planner 探测选档 → describe（用选出的档位）→ init → 多路径传输 → complete。
     * 原生 planner 不可用时退回固定分片单路径，保证能传。
     */
    private suspend fun runPlanned(
        file: File,
        remoteDir: String,
        options: UploadOptions,
        onProgress: (Long, Long) -> Unit
    ): UploadCompleteData = coroutineScope {
        val total = file.length()
        val pool = buildNodePool()
        if (!SyncPlanner.available || pool.isEmpty()) {
            // 原生 planner 不可用（.so 缺失/过旧）：退回固定分片单路径
            Log.w(TAG, "[upload] planner 原生不可用，退回固定分片上传")
            val desc = describe(file, DEFAULT_CHUNK_SIZE)
            onProgress(0, desc.totalSize)
            return@coroutineScope runFixedWithRetry(desc, file, remoteDir, options, onProgress)
        }

        val handle = SyncPlanner.newPlanner(buildPlannerInput(total, pool))
        if (handle == 0L) {
            Log.w(TAG, "[upload] planner 创建失败，退回固定分片上传")
            val desc = describe(file, DEFAULT_CHUNK_SIZE)
            onProgress(0, desc.totalSize)
            return@coroutineScope runFixedWithRetry(desc, file, remoteDir, options, onProgress)
        }
        try {
            runPlannerLoop(handle, pool, file, remoteDir, options, onProgress)
        } finally {
            // 成功/失败都留下本次测速样本（init 失败也留，下次换节点重试 10min 内免重复探测）
            SyncPlanner.export(handle)?.let { SyncPlanner.saveState(it) }
            SyncPlanner.free(handle)
        }
    }

    /** 构造 planner 节点池：当前 baseUrl 为激活节点（id="active"，main_node），叠加内置多路径节点；
     *  URL 与激活节点相同的内置节点去重跳过，其子节点的父引用重指到保留节点。 */
    private fun buildNodePool(): List<PlannerNode> {
        val builtIns = listOf(
            PlannerNode("ddns", "https://ddns.sunyuanling.cn/file"),
            PlannerNode("jp", "https://jp.sunyuanling.cn:8443/file"),
            PlannerNode("hk", "https://hk.sunyuanling.cn:8443/file", parent = "ddns"),
        )
        val pool = mutableListOf(PlannerNode("active", AppConfig.getBaseUrl()))
        val idRemap = HashMap<String, String>()
        for (n in builtIns) {
            val dup = pool.firstOrNull { it.url.trimEnd('/') == n.url.trimEnd('/') }
            if (dup != null) {
                idRemap[n.id] = dup.id
                continue
            }
            val parent = n.parent?.let { idRemap[it] ?: it }
            // 父节点不在池里父链折算无从谈起（视为无约束），这类子节点一并跳过
            if (parent != null && pool.none { it.id == parent }) continue
            pool.add(n.copy(parent = parent))
        }
        return pool
    }

    /** PlannerInput JSON：节点池 + 上次持久化样本（SharedPreferences 里的 export）。 */
    private fun buildPlannerInput(totalSize: Long, pool: List<PlannerNode>): String {
        val stateEl = SyncPlanner.loadState()
            ?.let { runCatching { plannerJson.parseToJsonElement(it) }.getOrNull() }
            ?.let { it as? JsonObject }
            ?: JsonObject(emptyMap())
        return buildJsonObject {
            put("total_size", totalSize)
            put("main_node", "active")
            put("now_ms", System.currentTimeMillis())
            put("nodes", JsonArray(pool.map { n ->
                buildJsonObject {
                    put("id", n.id)
                    put("url", n.url)
                    n.parent?.let { put("parent", it) }
                }
            }))
            put("state", stateEl)
            put("adjust_cooldown_ms", PLANNER_ADJUST_COOLDOWN_MS)
        }.toString()
    }

    /** 探测 → 选档 → describe → init → 传输。planner 句柄由调用方负责释放。 */
    private suspend fun CoroutineScope.runPlannerLoop(
        handle: Long,
        pool: List<PlannerNode>,
        file: File,
        remoteDir: String,
        options: UploadOptions,
        onProgress: (Long, Long) -> Unit
    ): UploadCompleteData {
        // ── 探测阶段：planner 一次只派一个探测，inline 执行回结果后再拿下一个 ──
        // 聚合每个节点的 rtt/bps 样本，finalize 后统一打一行探测日志。
        val probeRtt = HashMap<String, Long>()
        val probeBps = HashMap<String, Double>()
        while (true) {
            when (val task = nextTask(handle)) {
                is PlannerTask.Probe -> {
                    val (reportJson, rtt, bps) = runProbe(task)
                    rtt?.let { probeRtt[task.node] = it }
                    bps?.let { probeBps[task.node] = it }
                    reportOrThrow(handle, reportJson)
                }
                // 探测 inline 执行，不会有未决探测：Wait = 队列探完、规划完成
                PlannerTask.Wait -> break
                is PlannerTask.Failed -> throw Exception(task.reason)
                else -> throw Exception("上传规划异常任务: $task")
            }
        }
        val chunkSize = SyncPlanner.chunkSize(handle)
        if (chunkSize <= 0L) throw Exception("上传规划失败：没有可用路径（全部节点测速失败）")
        logPlan(handle, pool, probeRtt, probeBps, chunkSize)

        // describe 依赖分片档位，探测选档完成后再算哈希（一趟顺序读，叶子+树根+整文件）
        val desc = describe(file, chunkSize.toInt())
        onProgress(0, desc.totalSize)

        val init = callInit(desc, file.name, remoteDir, options)

        // 秒传：服务端在 init 阶段已复制落盘并完成同步派发，【没有建会话】——
        // 不能调 complete（会 404 会话不存在），结果就地合成。
        if (init.instant) {
            onProgress(desc.totalSize, desc.totalSize)
            return UploadCompleteData(
                fileName = file.name,
                storagePath = remoteDir,
                fileSize = desc.totalSize,
                fileHash = desc.fileHashHex,
                synced = true,
            )
        }

        return runTransfer(handle, desc, init, file, remoteDir, options, onProgress)
    }

    /** 传输阶段：每拿到 Chunk 任务就 async 发送，结果经 Channel 喂回循环；Wait 挂在 Channel 上。 */
    private suspend fun CoroutineScope.runTransfer(
        handle: Long,
        desc: Description,
        init: UploadInitData,
        file: File,
        remoteDir: String,
        options: UploadOptions,
        onProgress: (Long, Long) -> Unit
    ): UploadCompleteData {
        val missing = if (init.missing.isNotEmpty()) init.missing else (0 until desc.chunkCount).toList()
        // 已落盘分片估算基线（用片数×chunkSize 上界，complete 时会自然修正）
        val bytesSent = AtomicLong((desc.chunkCount - missing.size).toLong() * desc.chunkSize)
        onProgress(minOf(bytesSent.get(), desc.totalSize), desc.totalSize)

        // 首个会话接入：begin_transfer 与 resume 同效（选档已在探测后 finalize），统一走 resume
        resumeOrThrow(handle, init.uploadId, missing)

        val channel = Channel<ChunkOutcome>(capacity = 64)
        var uploadId = init.uploadId
        var winSnapshot = SyncPlanner.windows(handle)
        while (true) {
            when (val task = nextTask(handle)) {
                is PlannerTask.Chunk -> {
                    val uid = uploadId // 闭包捕获当前值：Reinit 后在途旧会话分片不受影响
                    val t = task
                    launch {
                        val outcome = try {
                            executeChunk(file, uid, desc.chunkSize, desc.totalSize, t)
                        } catch (ce: CancellationException) {
                            throw ce
                        } catch (e: Exception) {
                            ChunkOutcome.Fatal("分片 ${t.index} 执行失败: ${e.message}")
                        }
                        channel.send(outcome)
                    }
                }
                PlannerTask.Wait -> {
                    // 全部窗口占满/队列派空：等在途分片的回报；停滞保护防挂死
                    val outcome = withTimeoutOrNull(STALL_TIMEOUT_MS) { channel.receive() }
                        ?: throw Exception("上传调度停滞：在途分片长时间无响应")
                    when (outcome) {
                        is ChunkOutcome.Fatal -> throw Exception(outcome.message)
                        is ChunkOutcome.Report -> {
                            val type = outcome.json["type"]?.jsonPrimitive?.content ?: "unknown"
                            if (type == "chunk_ok") {
                                val bytes = outcome.json["bytes"]?.jsonPrimitive?.longOrNull ?: 0L
                                val now = minOf(bytesSent.addAndGet(bytes), desc.totalSize)
                                onProgress(now, desc.totalSize)
                            }
                            reportOrThrow(handle, outcome.json)
                            logWindowChanges(handle, winSnapshot, reportReason(type))
                                .also { winSnapshot = it }
                        }
                    }
                }
                PlannerTask.Reinit -> {
                    Log.i(TAG, "[upload] 会话过期，重新 init 后续传: ${file.name}")
                    val init2 = callInit(desc, file.name, remoteDir, options)
                    if (init2.instant) {
                        // 重传期间已由其它设备传完同一内容：按完成处理（不能再走 complete）
                        onProgress(desc.totalSize, desc.totalSize)
                        return UploadCompleteData(
                            fileName = file.name,
                            storagePath = remoteDir,
                            fileSize = desc.totalSize,
                            fileHash = desc.fileHashHex,
                            synced = true,
                        )
                    }
                    uploadId = init2.uploadId
                    val missing2 = if (init2.missing.isNotEmpty()) init2.missing else (0 until desc.chunkCount).toList()
                    // 进度基线按新的 missing 重算（服务端已收到的片不再重复计数）
                    bytesSent.set((desc.chunkCount - missing2.size).toLong() * desc.chunkSize)
                    onProgress(minOf(bytesSent.get(), desc.totalSize), desc.totalSize)
                    resumeOrThrow(handle, uploadId, missing2)
                }
                PlannerTask.Done -> return completeUpload(uploadId, options.deviceId)
                is PlannerTask.Failed -> throw Exception(task.reason)
                is PlannerTask.Probe -> throw Exception("上传规划异常：传输阶段出现探测任务")
            }
        }
    }

    /** 执行一个探测任务：POST 任务指定节点的测速接口，把响应当 RTT/带宽样本回报。
     *  tiny 的耗时当 RTT；吞吐样本由 planner 按 bytes/elapsed 换算 bps。
     *  请求失败/响应无 node 字段 → ProbeFailed。 */
    private suspend fun runProbe(task: PlannerTask.Probe): Triple<JsonObject, Long?, Double?> {
        val url = task.url.trimEnd('/') + SPEEDTEST_PATH + "?bytes=${task.bytes}"
        val started = SystemClock.elapsedRealtime()
        return try {
            val token = Request.getToken()
            val body = ByteArray(task.bytes.toInt()).toRequestBody("application/octet-stream".toMediaType())
            val builder = OkRequest.Builder().url(url).post(body)
            token?.let { builder.header("Token", it) }
            builder.header("Device-Id", Request.deviceId())
            Request.client.newCall(builder.build()).execute().use { resp ->
                val text = resp.body?.string().orEmpty()
                val elapsed = (SystemClock.elapsedRealtime() - started).coerceAtLeast(1)
                val obj = runCatching { plannerJson.parseToJsonElement(text).jsonObject }.getOrNull()
                val code = obj?.get("code")?.jsonPrimitive?.intOrNull
                val backend = obj?.get("data")?.jsonObject?.get("node")?.jsonPrimitive?.contentOrNull
                if (resp.isSuccessful && code == 200 && !backend.isNullOrEmpty()) {
                    val rtt = if (task.kind == ProbeKind.TINY) elapsed else null
                    val bps = if (task.kind == ProbeKind.THROUGHPUT) {
                        task.bytes.toDouble() / (elapsed / 1000.0)
                    } else null
                    Triple(probeOkJson(task, elapsed, backend), rtt, bps)
                } else {
                    Triple(probeFailedJson(task), null, null)
                }
            }
        } catch (ce: CancellationException) {
            throw ce
        } catch (e: Exception) {
            Triple(probeFailedJson(task), null, null)
        }
    }

    /** 执行一个分片任务：定位读出字节 → POST 到任务给的 node url → 分类成 Report。
     *  2xx+code200→ChunkOk（bytes=实际字节，供进度与 planner 的 EMA）；
     *  422→ChunkDataError；404→ChunkSessionGone；超时/传输错误→ChunkCongested。 */
    private suspend fun executeChunk(
        file: File, uploadId: String, chunkSize: Int, totalSize: Long, task: PlannerTask.Chunk
    ): ChunkOutcome {
        val offset = task.index.toLong() * chunkSize
        val len = minOf(chunkSize.toLong(), totalSize - offset)
        if (len < 0L) return ChunkOutcome.Fatal("分片 ${task.index} 越界 (offset=$offset, total=$totalSize)")
        val data = ByteArray(len.toInt())
        RandomAccessFile(file, "r").use { raf ->
            raf.seek(offset)
            raf.readFully(data)
        }

        // Chunk 必须打到 planner 给的 node url；ApiRoutes 常量不带 /v1 前缀（Request.baseUrl 才拼），直连节点 URL 时补上。
        // per-call clone client 设任务超时（连接池/调度器与全局 client 共享）。
        val url = task.url.trimEnd('/') + "/v1" + ApiRoutes.FILE_UPLOAD_CHUNK +
                "?upload_id=${URLEncoder.encode(uploadId, "UTF-8")}&index=${task.index}"
        val callClient = Request.client.newBuilder()
            .readTimeout(task.timeoutMs, TimeUnit.MILLISECONDS)
            .build()
        val started = SystemClock.elapsedRealtime()
        return try {
            val token = Request.getToken()
            val body = data.toRequestBody("application/octet-stream".toMediaType())
            val builder = OkRequest.Builder().url(url).post(body)
            token?.let { builder.header("Token", it) }
            builder.header("Device-Id", Request.deviceId())
            callClient.newCall(builder.build()).execute().use { resp ->
                val elapsed = (SystemClock.elapsedRealtime() - started).coerceAtLeast(1)
                when {
                    resp.code == 422 -> ChunkOutcome.Report(seqReport("chunk_data_error", task.seq))
                    resp.code == 404 -> ChunkOutcome.Report(seqReport("chunk_session_gone", task.seq))
                    !resp.isSuccessful -> ChunkOutcome.Report(seqReport("chunk_congested", task.seq))
                    else -> {
                        // HTTP 200：服务端业务码在 body 里（非 200 按拥塞处理，占一次派发预算）
                        val text = resp.body?.string().orEmpty()
                        val obj = runCatching { plannerJson.parseToJsonElement(text).jsonObject }.getOrNull()
                        when (obj?.get("code")?.jsonPrimitive?.intOrNull) {
                            200 -> ChunkOutcome.Report(buildJsonObject {
                                put("type", "chunk_ok")
                                put("seq", task.seq)
                                put("bytes", len)
                                put("elapsed_ms", elapsed)
                            })
                            422 -> ChunkOutcome.Report(seqReport("chunk_data_error", task.seq))
                            404 -> ChunkOutcome.Report(seqReport("chunk_session_gone", task.seq))
                            else -> ChunkOutcome.Report(seqReport("chunk_congested", task.seq))
                        }
                    }
                }
            }
        } catch (ce: CancellationException) {
            throw ce
        } catch (e: Exception) {
            // 超时/传输层错误 = 拥塞信号，交给 planner 减半窗口并重排
            ChunkOutcome.Report(seqReport("chunk_congested", task.seq))
        }
    }

    // ---------------------------------------------------------------- planner JSON 工具

    /** 解析 Task JSON（变体对齐 upload_planner::Task 的 serde 形态）。 */
    private fun parseTask(jsonText: String): PlannerTask {
        val obj = plannerJson.parseToJsonElement(jsonText).jsonObject
        return when (obj["type"]?.jsonPrimitive?.content) {
            "probe" -> PlannerTask.Probe(
                node = obj["node"]?.jsonPrimitive?.content ?: "",
                url = obj["url"]?.jsonPrimitive?.content ?: "",
                kind = if (obj["kind"]?.jsonPrimitive?.content == "throughput") {
                    ProbeKind.THROUGHPUT
                } else ProbeKind.TINY,
                bytes = obj["bytes"]?.jsonPrimitive?.longOrNull ?: 0L,
            )
            "chunk" -> PlannerTask.Chunk(
                seq = obj["seq"]?.jsonPrimitive?.longOrNull ?: 0L,
                node = obj["node"]?.jsonPrimitive?.content ?: "",
                url = obj["url"]?.jsonPrimitive?.content ?: "",
                index = obj["index"]?.jsonPrimitive?.intOrNull ?: 0,
                timeoutMs = obj["timeout_ms"]?.jsonPrimitive?.longOrNull ?: 30_000L,
                bytes = obj["bytes"]?.jsonPrimitive?.longOrNull ?: 0L,
            )
            "reinit" -> PlannerTask.Reinit
            "wait" -> PlannerTask.Wait
            "done" -> PlannerTask.Done
            "failed" -> PlannerTask.Failed(obj["reason"]?.jsonPrimitive?.content ?: "未知错误")
            else -> PlannerTask.Failed("无法识别的任务: ${jsonText.take(100)}")
        }
    }

    private fun nextTask(handle: Long): PlannerTask {
        val text = SyncPlanner.next(handle)
            ?: return PlannerTask.Failed("读取 planner 任务失败（原生异常）")
        return runCatching { parseTask(text) }
            .getOrElse { PlannerTask.Failed("任务解析失败: ${it.message}") }
    }

    private fun reportOrThrow(handle: Long, reportJson: JsonObject) {
        if (!SyncPlanner.report(handle, reportJson.toString())) {
            throw Exception("planner 回报写入失败")
        }
    }

    private fun resumeOrThrow(handle: Long, uploadId: String, missing: List<Int>) {
        val missingJson = JsonArray(missing.map { JsonPrimitive(it) }).toString()
        if (!SyncPlanner.resume(handle, uploadId, missingJson)) {
            throw Exception("planner 会话接入失败")
        }
    }

    private fun seqReport(type: String, seq: Long) = buildJsonObject {
        put("type", type)
        put("seq", seq)
    }

    private fun probeOkJson(task: PlannerTask.Probe, elapsedMs: Long, backendNode: String) = buildJsonObject {
        put("type", "probe_ok")
        put("node", task.node)
        put("kind", if (task.kind == ProbeKind.TINY) "tiny" else "throughput")
        put("bytes", task.bytes)
        put("elapsed_ms", elapsedMs)
        put("backend_node", backendNode)
    }

    private fun probeFailedJson(task: PlannerTask.Probe) = buildJsonObject {
        put("type", "probe_failed")
        put("node", task.node)
        put("kind", if (task.kind == ProbeKind.TINY) "tiny" else "throughput")
    }

    // ---------------------------------------------------------------- planner 日志

    /** 探测完成后打一行规划日志：每节点 rtt=微样本 b=实测带宽 eff=父链折算后带宽
     *  chunk=选出的档位 conc=初始窗口。
     *  示例：[upload] probe node=ddns rtt=20ms b=1.3MB/s eff=1.3MB/s chunk=8MB conc=1 */
    private fun logPlan(
        handle: Long,
        pool: List<PlannerNode>,
        probeRtt: Map<String, Long>,
        probeBps: Map<String, Double>,
        chunkSize: Long
    ) {
        val eff = effectiveBps(pool, probeBps)
        val windows = SyncPlanner.windows(handle)
        for (n in pool) {
            val rtt = probeRtt[n.id] ?: continue
            val b = probeBps[n.id] ?: continue
            val e = eff[n.id] ?: b
            val conc = windows.firstOrNull { it.first == n.id }?.second ?: 0
            Log.i(TAG, "[upload] probe node=${n.id} rtt=${rtt}ms b=${fmtBps(b)} " +
                    "eff=${fmtBps(e)} chunk=${fmtChunk(chunkSize)} conc=$conc")
        }
    }

    /** 窗口变化日志：[upload] node=jp window 8→4 (timeout) */
    private fun logWindowChanges(
        handle: Long,
        snapshot: List<Pair<String, Int>>,
        reason: String
    ): List<Pair<String, Int>> {
        val now = SyncPlanner.windows(handle)
        for ((id, w) in now) {
            val old = snapshot.firstOrNull { it.first == id }?.second
            if (old != null && old != w) {
                Log.i(TAG, "[upload] node=$id window $old→$w ($reason)")
            }
        }
        return now
    }

    /** 窗口变化原因：跟随最近一次回报类型。 */
    private fun reportReason(type: String): String = when (type) {
        "chunk_ok" -> "streak"
        "chunk_congested" -> "timeout"
        "chunk_data_error" -> "data"
        "chunk_session_gone" -> "gone"
        else -> "probe"
    }

    /** 父链折算（对齐 upload_planner::combine_effective）：子节点有效带宽 = min(自身, 父节点有效)。
     *  父缺失/不健康则不加约束；measured 里没有的节点视为无有效带宽。 */
    private fun effectiveBps(pool: List<PlannerNode>, measured: Map<String, Double>): Map<String, Double> {
        val eff = HashMap<String, Double>()
        // 迭代加深：父链最长按节点数收敛
        for (i in pool.indices) {
            for (n in pool) {
                if (eff.containsKey(n.id)) continue
                val self = measured[n.id] ?: continue
                val parent = n.parent
                val v = if (parent == null) {
                    self
                } else {
                    val pe = eff[parent]
                    when {
                        pe != null -> minOf(self, pe)
                        measured.containsKey(parent) -> continue // 父还没算出来，下一轮再算
                        else -> self // 父无实测样本：不加约束
                    }
                }
                eff[n.id] = v
            }
        }
        return eff
    }

    /** bps → 人类可读（1.3MB/s / 500KB/s / 80B/s）。 */
    private fun fmtBps(bps: Double): String = when {
        bps >= 1_000_000.0 -> "%.1fMB/s".format(bps / 1_000_000.0)
        bps >= 1_000.0 -> "%.0fKB/s".format(bps / 1_000.0)
        else -> "%.0fB/s".format(bps)
    }

    /** 字节数 → 档位可读（8MB / 512KB / 64KB）。 */
    private fun fmtChunk(n: Long): String = when {
        n >= (1 shl 20) && n % (1 shl 20) == 0L -> "${n shr 20}MB"
        n >= (1 shl 10) && n % (1 shl 10) == 0L -> "${n shr 10}KB"
        else -> "${n}B"
    }

    // ---------------------------------------------------------------- 共享步骤

    private suspend fun callInit(
        desc: Description, name: String, remoteDir: String, options: UploadOptions
    ): UploadInitData {
        val res = FileApi.uploadInit(
            UploadInitParams(
                path = remoteDir,
                name = name,
                totalSize = desc.totalSize,
                chunkSize = desc.chunkSize.toLong(),
                chunkCount = desc.chunkCount,
                merkleRoot = desc.merkleRootHex,
                fileHash = desc.fileHashHex,
                leafHashes = desc.leafHashesHex,
                deviceId = options.deviceId
            )
        )
        return res.getOrElse { throw it }.data ?: throw Exception("init 响应为空")
    }

    private suspend fun completeUpload(uploadId: String, deviceId: String): UploadCompleteData {
        val res = FileApi.uploadComplete(UploadCompleteParams(uploadId = uploadId, deviceId = deviceId))
        val data = res.getOrElse { throw it }.data ?: throw Exception("complete 响应为空")
        return data
    }
}
