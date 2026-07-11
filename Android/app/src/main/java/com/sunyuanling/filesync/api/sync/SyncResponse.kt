// api/sync/SyncResponse.kt
// 职责：同步域响应 DTO，字段与后端 GORM 模型 json tag 严格对齐
// （new_server/internal/model/SyncTask.go / SyncConflict.go / SyncFolder.go）。
// 时间字段为 Go RFC3339Nano 字符串，解析用 DeviceMonitorViewModel.parseIsoToMillis。
package com.sunyuanling.filesync.api.sync

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** 同步任务（GET /sync/tasks、GET /sync/tasks/pending） */
@Serializable
data class SyncTaskInfo(
    val id: Long = 0,
    @SerialName("user_id") val userId: Long = 0,
    @SerialName("source_device_id") val sourceDeviceId: String = "",
    @SerialName("target_device_id") val targetDeviceId: String = "",
    @SerialName("folder_id") val folderId: Long = 0,
    @SerialName("file_id") val fileId: Long = 0,
    @SerialName("task_type") val taskType: String = "",
    @SerialName("sync_status") val syncStatus: String = "",
    val direction: String = "",
    @SerialName("relative_path") val relativePath: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_hash") val fileHash: String? = null,
    @SerialName("source_hash") val sourceHash: String? = null,
    @SerialName("base_hash") val baseHash: String? = null,
    val conflict: Boolean = false,
    val progress: Int = 0,
    val priority: Int = 0,
    @SerialName("retry_count") val retryCount: Int = 0,
    @SerialName("max_retry") val maxRetry: Int = 0,
    @SerialName("error_message") val errorMessage: String? = null,
    @SerialName("started_at") val startedAt: String? = null,
    @SerialName("completed_at") val completedAt: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

/** 同步冲突记录（GET /sync/conflicts） */
@Serializable
data class SyncConflictInfo(
    val id: Long = 0,
    @SerialName("user_id") val userId: Long = 0,
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("folder_id") val folderId: Long = 0,
    @SerialName("file_id") val fileId: Long = 0,
    @SerialName("relative_path") val relativePath: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("server_hash") val serverHash: String? = null,
    @SerialName("local_hash") val localHash: String? = null,
    @SerialName("base_hash") val baseHash: String? = null,
    @SerialName("server_version") val serverVersion: Long = 0,
    val status: String = "",
    val resolution: String? = null,
    @SerialName("resolved_at") val resolvedAt: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

/** 同步文件夹（GET /sync/folders） */
@Serializable
data class SyncFolderInfo(
    val id: Long = 0,
    @SerialName("user_id") val userId: Long = 0,
    val name: String = "",
    @SerialName("local_path") val localPath: String = "",
    @SerialName("remote_path") val remotePath: String = "",
    val direction: String = "",
    val enabled: Boolean = true,
    val excludes: String = "",
    @SerialName("owner_device_id") val ownerDeviceId: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

/** 解决冲突请求体（POST /sync/conflicts/:id/resolve） */
@Serializable
data class ResolveConflictParams(
    /** accept_server（采用服务器版本）/ keep_local（保留本地版本重新上传） */
    val resolution: String,
)
