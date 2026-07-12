// sync/SyncBaseStore.kt
// 职责：同步基线（base）——每个文件「上次同步后已知的服务端 trunk hash」+ 本地 stat 快照。
//
// 用途：
// - CAS：上报 file_changed 时带 base_hash，服务端据此判快进/冲突；
// - 变更检测降噪：size+mtime 未变的文件跳过重算 hash（追赶扫描的大头开销）。
//
// 对标桌面端 base_store（config/state.json），持久化 <ExternalStorage>/FileSync/sync_base.json。
package com.sunyuanling.filesync.sync

import android.os.Environment
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File
import java.util.concurrent.ConcurrentHashMap

/** 一条基线：服务端 trunk hash + 记录时的本地 stat。 */
@Serializable
data class BaseEntry(
    val hash: String,
    val size: Long = 0,
    val mtime: Long = 0,
)

object SyncBaseStore {

    private const val TAG = "SyncBaseStore"
    private const val SAVE_DEBOUNCE_MS = 1_500L

    private val storeFile: File
        get() = File(Environment.getExternalStorageDirectory(), "FileSync/sync_base.json")

    private val json = Json { ignoreUnknownKeys = true }
    private val map = ConcurrentHashMap<String, BaseEntry>()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    @Volatile private var saveJob: Job? = null

    private fun key(folderId: Long, rel: String) = "$folderId|$rel"

    fun load() {
        runCatching {
            if (!storeFile.exists()) return
            val loaded = json.decodeFromString<Map<String, BaseEntry>>(storeFile.readText())
            map.clear()
            map.putAll(loaded)
        }.onFailure { Log.w(TAG, "读取基线失败: ${it.message}") }
    }

    /** 防抖落盘：同步高峰期避免每个文件写一次 JSON。 */
    private fun scheduleSave() {
        saveJob?.cancel()
        saveJob = scope.launch {
            delay(SAVE_DEBOUNCE_MS)
            flush()
        }
    }

    fun flush() {
        runCatching {
            storeFile.parentFile?.mkdirs()
            storeFile.writeText(json.encodeToString(map.toMap()))
        }.onFailure { Log.w(TAG, "保存基线失败: ${it.message}") }
    }

    fun get(folderId: Long, rel: String): BaseEntry? = map[key(folderId, rel)]

    /** 已知的服务端 hash（CAS base），无基线返回空串（协议里 create 留空）。 */
    fun hashOf(folderId: Long, rel: String): String = get(folderId, rel)?.hash ?: ""

    fun set(folderId: Long, rel: String, hash: String, size: Long, mtime: Long) {
        map[key(folderId, rel)] = BaseEntry(hash, size, mtime)
        scheduleSave()
    }

    fun remove(folderId: Long, rel: String) {
        map.remove(key(folderId, rel))
        scheduleSave()
    }

    fun clearFolder(folderId: Long) {
        val prefix = "$folderId|"
        val toRemove = map.keys.filter { it.startsWith(prefix) }
        toRemove.forEach { map.remove(it) }
        if (toRemove.isNotEmpty()) scheduleSave()
    }

    /** 该 folder 的全部基线（追赶扫描比对用），key 为 relative_path。 */
    fun folderSnapshot(folderId: Long): Map<String, BaseEntry> {
        val prefix = "$folderId|"
        val out = HashMap<String, BaseEntry>()
        for ((k, v) in map) {
            if (k.startsWith(prefix)) out[k.substring(prefix.length)] = v
        }
        return out
    }
}
