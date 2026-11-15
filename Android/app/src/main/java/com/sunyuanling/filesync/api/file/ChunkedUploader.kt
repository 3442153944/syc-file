// Platform facade; all chunk scheduling, reading, retry and completion logic is in sys_core.
package com.sunyuanling.filesync.api.file
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.core.SyncCore
import com.sunyuanling.filesync.core.SysUpload
import kotlinx.coroutines.CancellationException
import java.io.File
class ChunkVerifyException(val index: Int, message: String) : Exception(message)
class SessionGoneException(message: String) : Exception(message)
object ChunkedUploader {
    const val DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024
    const val DEFAULT_CONCURRENCY = 3
    data class UploadOptions(
        /** 分片大小：null=自适应（planner 按大小定档 + 多路径调度 + 传输期后台探测，默认）；
         *  显式值=固定分片，走激活节点的旧单路径行为。 */
        val chunkSize: Int? = null,
        /** 并发覆盖：null=planner 的 per-node AIMD 窗口；显式值=固定路径的 Semaphore 并发数。 */
        val concurrency: Int? = null,
        /** 本机设备 id，服务端派发同步任务时排除源设备。空字符串表示不传。 */
        val deviceId: String = "",
        /** 快传与普通上传共用调度核心，仅初始化和完成结果不同。 */
        val quickShare: Boolean = false,
        val baseUrl: String = AppConfig.getBaseUrl(),
        val resume: UploadInitData? = null,
        val onSession: suspend (String) -> Unit = {},
        /** 平台节点注册表可显式注入；规划与选片仍由 Rust 决定。 */
        val nodes: List<PlannerNode>? = null
    )

    data class Description(
        val totalSize: Long,
        val chunkSize: Int,
        val chunkCount: Int,
        val leafHashesHex: List<String>,
        val merkleRootHex: String,
        val fileHashHex: String
    )


    data class PlannerNode(val id: String, val url: String, val parent: String? = null)
    fun describe(file: File, chunkSize: Int = DEFAULT_CHUNK_SIZE): Description =
        SyncCore.describe(file, chunkSize) ?: error("sys_core 文件描述失败，请检查原生库和源文件")
    suspend fun upload(file: File, remoteDir: String, options: UploadOptions = UploadOptions(),
        onProgress: (Long, Long) -> Unit = { _, _ -> }): Result<UploadCompleteData> = try {
        Result.success(SysUpload.upload(file, remoteDir, options, onProgress))
    } catch (ce: CancellationException) { throw ce }
      catch (e: Exception) { Result.failure(e) }
    internal fun buildNodePool(baseUrl: String): List<PlannerNode> {
        val builtIns = listOf(
            PlannerNode("ddns", "https://ddns.sunyuanling.cn/file"),
            PlannerNode("jp", "https://jp.sunyuanling.cn:8443/file"),
            PlannerNode("hk", "https://hk.sunyuanling.cn:8443/file", parent = "ddns"),
        )
        val pool = mutableListOf(PlannerNode("active", baseUrl))
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
        com.sunyuanling.filesync.network.NodeFailover.backups.lineSequence()
            .filter { it.isNotBlank() }.forEachIndexed { index, url ->
                if (pool.none { it.url.trimEnd('/') == url.trimEnd('/') }) pool.add(PlannerNode("custom-$index", url))
            }
        return pool
    }

}
