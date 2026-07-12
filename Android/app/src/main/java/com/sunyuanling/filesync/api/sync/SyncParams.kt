// api/sync/SyncParams.kt
// 职责：同步域请求参数 DTO，对齐后端 internal/sync/types.go 与 handler.go 的绑定结构。
package com.sunyuanling.filesync.api.sync

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * 单文件变更上报（POST /sync/notify）。
 * 对应 Go 的 struct{DeviceID; FileChangeReport}（内嵌展平）。
 * 时序约束：action=create/modify 必须先分片上传成功再上报。
 */
@Serializable
data class SyncNotifyParams(
    @SerialName("device_id") val deviceId: String,
    @SerialName("folder_id") val folderId: Long,
    /** 相对 folder 远端根，'/' 分隔，禁 '..' */
    @SerialName("relative_path") val relativePath: String,
    @SerialName("file_name") val fileName: String,
    /** create / modify / delete */
    val action: String,
    @SerialName("file_size") val fileSize: Long = 0,
    /** 新内容 blake3 hex（delete/dir 可空） */
    @SerialName("file_hash") val fileHash: String = "",
    /** 修改前客户端看到的 trunk hash（CAS）；create 留空 */
    @SerialName("base_hash") val baseHash: String = "",
    @SerialName("is_dir") val isDir: Boolean = false,
    val mtime: Long = 0,
)

/** 扫描清单单项（对齐 Go ScanItem）。 */
@Serializable
data class ScanItemDto(
    @SerialName("relative_path") val relativePath: String,
    @SerialName("file_name") val fileName: String,
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_hash") val fileHash: String = "",
    @SerialName("is_dir") val isDir: Boolean = false,
    val mtime: Long = 0,
)

/** 全量扫描上报（POST /sync/scan）。 */
@Serializable
data class SyncScanParams(
    @SerialName("device_id") val deviceId: String,
    @SerialName("folder_id") val folderId: Long,
    val items: List<ScanItemDto>,
)

@Serializable
data class TaskCompleteParams(@SerialName("file_hash") val fileHash: String = "")

@Serializable
data class TaskFailedParams(val error: String = "")

@Serializable
data class TaskBlockedParams(val reason: String = "")
