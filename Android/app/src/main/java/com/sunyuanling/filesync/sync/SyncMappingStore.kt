// sync/SyncMappingStore.kt
// 职责：本设备的「同步文件夹 → 本地目录」映射。
//
// 同步文件夹（SyncFolder）由 Windows 端创建、存服务器 MySQL，是全用户共享的定义；
// 而**本地映射是纯设备私有**——每台设备自己决定把某个 folder 映射到哪个本地目录、
// 是否在本机启用。映射持久化在 <ExternalStorage>/FileSync/sync_mappings.json。
package com.sunyuanling.filesync.sync

import android.os.Environment
import android.util.Log
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File

/** 一条设备本地映射。 */
@Serializable
data class SyncMapping(
    @SerialName("folder_id") val folderId: Long,
    /** 本地根目录绝对路径 */
    @SerialName("local_path") val localPath: String,
    /** 本机是否启用该文件夹的同步 */
    val enabled: Boolean = true,
)

object SyncMappingStore {

    private const val TAG = "SyncMappingStore"

    private val storeFile: File
        get() = File(Environment.getExternalStorageDirectory(), "FileSync/sync_mappings.json")

    private val json = Json { ignoreUnknownKeys = true; prettyPrint = true }

    private val _mappings = MutableStateFlow<Map<Long, SyncMapping>>(emptyMap())
    val mappings: StateFlow<Map<Long, SyncMapping>> = _mappings.asStateFlow()

    fun load() {
        runCatching {
            if (!storeFile.exists()) return
            val list = json.decodeFromString<List<SyncMapping>>(storeFile.readText())
            _mappings.value = list.associateBy { it.folderId }
        }.onFailure { Log.w(TAG, "读取映射失败: ${it.message}") }
    }

    private fun persist() {
        runCatching {
            storeFile.parentFile?.mkdirs()
            storeFile.writeText(json.encodeToString(_mappings.value.values.toList()))
        }.onFailure { Log.w(TAG, "保存映射失败: ${it.message}") }
    }

    fun mappingFor(folderId: Long): SyncMapping? = _mappings.value[folderId]

    /** 已启用且本地目录非空的映射。 */
    fun enabledMappingFor(folderId: Long): SyncMapping? =
        _mappings.value[folderId]?.takeIf { it.enabled && it.localPath.isNotEmpty() }

    fun put(mapping: SyncMapping) {
        _mappings.value = _mappings.value + (mapping.folderId to mapping)
        persist()
    }

    fun remove(folderId: Long) {
        _mappings.value = _mappings.value - folderId
        persist()
    }

    /** 建议的默认本地路径：<ExternalStorage>/FileSync/sync/<文件夹名>。 */
    fun defaultLocalPath(folderName: String, folderId: Long): String {
        val safe = folderName.ifBlank { "folder_$folderId" }
            .replace(Regex("""[\\/:*?"<>|]"""), "_")
        return File(Environment.getExternalStorageDirectory(), "FileSync/sync/$safe").absolutePath
    }
}
