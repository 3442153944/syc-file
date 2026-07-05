// api/file/ChunkedUploader.kt
// 分片上传编排：算描述信息（叶子/树根/整文件哈希）→ init →（秒传则完成）→ 乱序并发补传缺失分片 → complete。
//
// 设计要点：
// - 并发：后端 chunk 接口支持乱序/可并发，本端用 Semaphore 限流的协程池并发上传，大幅提速大文件。
// - 进度：字节级，基于已成功分片累计字节数回调；init 阶段先置「已落盘分片」估算基线。
// - 取消：本函数为 suspend，调用方所在协程被 cancel 即停止派发新分片；已在途的 chunk 请求随 IO 抛
//   CancellationException。每个分片任务开头 ensureActive()，确保 cancel 后立即响应。
// - 会话过期：任一分片收 404 → SessionGoneException，由外层 upload() 捕获后自动重新 init 整流程一次
//   （不会重算哈希；二次仍失败则原样抛出）。
package com.sunyuanling.filesync.api.file

import com.sunyuanling.filesync.util.Blake3Util
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import java.io.File
import java.io.RandomAccessFile
import java.util.concurrent.atomic.AtomicLong
import kotlin.coroutines.coroutineContext

/** 分片校验失败：服务端拒收该片，需重传。 */
class ChunkVerifyException(val index: Int, message: String) : Exception(message)

/** 会话不存在（过期/被清）：需重新 init 整个上传。 */
class SessionGoneException(message: String) : Exception(message)

object ChunkedUploader {

    /** 默认分片 4MiB。 */
    const val DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024

    /** 默认并发分片数。 */
    const val DEFAULT_CONCURRENCY = 3

    /** 单片校验失败时的重传次数。 */
    private const val CHUNK_RETRY = 3

    data class UploadOptions(
        val chunkSize: Int = DEFAULT_CHUNK_SIZE,
        val concurrency: Int = DEFAULT_CONCURRENCY,
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
     * 一趟顺序读文件：算每片 blake3 叶子 + 整文件流式 blake3，再由叶子构 Merkle 树根。
     */
    fun describe(file: File, chunkSize: Int = DEFAULT_CHUNK_SIZE): Description {
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
            val desc = describe(file, options.chunkSize)
            onProgress(0, desc.totalSize)
            val data = try {
                runUploadOnce(desc, file, remoteDir, options, onProgress)
            } catch (e: SessionGoneException) {
                // 会话过期：重新 init 整流程一次（不会重算哈希）
                runUploadOnce(desc, file, remoteDir, options, onProgress)
            }
            Result.success(data)
        } catch (ce: CancellationException) {
            // 取消向上传播，绝不能被通用 catch 吞掉
            throw ce
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /**
     * 一次完整的「init → 乱序并发补传 → complete」流程。
     * 会话过期时抛 SessionGoneException 由上层重试。
     */
    private suspend fun runUploadOnce(
        desc: Description,
        file: File,
        remoteDir: String,
        options: UploadOptions,
        onProgress: (Long, Long) -> Unit
    ): UploadCompleteData = coroutineScope {
        val init = callInit(desc, file.name, remoteDir, options)

        // 秒传：无需上传分片，服务端已落盘
        if (init.instant) {
            onProgress(desc.totalSize, desc.totalSize)
            return@coroutineScope completeUpload(init.uploadId, options.deviceId)
        }

        val missing = if (init.missing.isNotEmpty()) init.missing else (0 until desc.chunkCount).toList()
        // 已落盘分片估算基线（用片数×chunkSize 上界，complete 时会自然修正）
        val bytesSent = AtomicLong((desc.chunkCount - missing.size).toLong() * desc.chunkSize)
        onProgress(minOf(bytesSent.get(), desc.totalSize), desc.totalSize)

        val sem = Semaphore(options.concurrency.coerceAtLeast(1))
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

    /** 读出该片字节并上传（含分片级重试，校验失败抛 ChunkVerifyException，会话过期抛 SessionGoneException）。 */
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
        uploadChunkWithRetry(uploadId, index, data)
        return len
    }

    private suspend fun uploadChunkWithRetry(uploadId: String, index: Int, data: ByteArray) {
        var lastErr: Throwable? = null
        repeat(CHUNK_RETRY) {
            // 重试前再次确认未取消，避免 cancel 后还在重传
            coroutineContext.ensureActive()
            val r = FileApi.uploadChunk(uploadId, index, data)
            if (r.isSuccess) return
            lastErr = r.exceptionOrNull()
            // 会话没了没必要重试，直接上抛让外层重新 init
            if (lastErr is SessionGoneException) throw lastErr
            // ChunkVerifyException 保留重试：可能是网络传输损坏，相同字节重传有机会过；
            // 若是客户端叶子算错，init 阶段就会被树根校验拒，不会进到 chunk 这步
        }
        throw lastErr ?: Exception("分片 $index 上传失败")
    }

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
                leafHashes = desc.leafHashesHex
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
