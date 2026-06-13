// api/file/FileResponse.kt
// 职责：文件模块所有响应数据类。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag 及 gin.H 返回。
package com.sunyuanling.filesync.api.file

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// ==================== 通用分页 ====================

@Serializable
data class Pagination(
    val page: Int = 0,
    @SerialName("page_size") val pageSize: Int = 0,
    @SerialName("total_pages") val totalPages: Int = 0,
    @SerialName("has_more") val hasMore: Boolean = false
)

// ==================== 可用磁盘列表 ====================
// 对应后端 available_disks.go 中 diskInfoBrief / 外层 gin.H 返回

@Serializable
data class DiskInfo(
    val path: String = "",
    val mountpoint: String = "",
    val device: String = "",
    val fstype: String = "",
    val total: Long = 0,
    val free: Long = 0,
    val used: Long = 0,
    @SerialName("used_percent") val usedPercent: Double = 0.0,
    @SerialName("total_gb") val totalGb: String = "",
    @SerialName("free_gb") val freeGb: String = "",
    @SerialName("is_allowed") val isAllowed: Boolean = false,
    @SerialName("is_accessible") val isAccessible: Boolean = false,
    @SerialName("is_ssd") val isSsd: Boolean = false
)

@Serializable
data class AvailableDisksData(
    val total: Int = 0,
    @SerialName("allowed_count") val allowedCount: Int = 0,
    @SerialName("allowed_disks") val allowedDisks: List<DiskInfo> = emptyList(),
    @SerialName("all_disks") val allDisks: List<DiskInfo> = emptyList()
)

// ==================== 遍历目录 ====================
// 对应后端 traverse_directory.go 中 fileItem / traverseResponse

@Serializable
data class FileItem(
    val name: String = "",
    val path: String = "",
    @SerialName("is_dir") val isDir: Boolean = false,
    val size: Long = 0,
    /** 后端 time.Time → RFC3339 字符串 */
    @SerialName("mod_time") val modTime: String = "",
    val mode: String = "",
    val extension: String = "",
    @SerialName("children_count") val childrenCount: Int = 0
)

@Serializable
data class TraverseDirectoryData(
    @SerialName("current_path") val currentPath: String = "",
    @SerialName("parent_path") val parentPath: String = "",
    val items: List<FileItem> = emptyList(),
    @SerialName("total_count") val totalCount: Int = 0,
    @SerialName("dir_count") val dirCount: Int = 0,
    @SerialName("file_count") val fileCount: Int = 0,
    val pagination: Pagination? = null
)

// ==================== 下载 ====================
// GET /v1/file/download 返回文件流，无 JSON 响应体。
// 响应头 X-History-ID 包含下载历史记录 ID。

// ==================== 上传（check） ====================
// 对应后端 upload.go 中 handleCheck 返回

@Serializable
data class CheckFileData(
    val exists: Boolean = false,
    @SerialName("can_upload") val canUpload: Boolean = false,
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long? = null,
    val path: String = "",
    /** 后端 time.Time → RFC3339 字符串 */
    @SerialName("modified_at") val modifiedAt: String? = null
)

// ==================== 上传（upload） ====================
// 对应后端 upload.go 中 handleUpload 成功返回

@Serializable
data class UploadData(
    @SerialName("history_id") val historyId: Long = 0,
    @SerialName("file_name") val fileName: String = "",
    @SerialName("original_name") val originalName: String? = null,
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("storage_path") val storagePath: String = ""
)

// ==================== 下载历史 ====================
// 对应后端 download_history.go 及 model.DownloadHistory

@Serializable
data class DownloadHistoryItem(
    val id: Long = 0,
    @SerialName("user_id") val userId: Long = 0,
    @SerialName("device_id") val deviceId: Long = 0,
    @SerialName("file_id") val fileId: Long? = null,
    @SerialName("file_name") val fileName: String? = null,
    @SerialName("file_size") val fileSize: Long? = null,
    @SerialName("download_status") val downloadStatus: String = "",
    @SerialName("download_speed") val downloadSpeed: Long? = null,
    @SerialName("ip_address") val ipAddress: String? = null,
    @SerialName("started_at") val startedAt: String? = null,
    @SerialName("completed_at") val completedAt: String? = null,
    @SerialName("created_at") val createdAt: String = ""
)

@Serializable
data class DownloadHistoryData(
    val list: List<DownloadHistoryItem> = emptyList(),
    val total: Long = 0,
    val pageNum: Int = 0,
    val pageSize: Int = 0
)
@Serializable
/** 删除下载记录ids响应体*/
data class DeleteDownloadHistoryResponse(
    val data: Unit? = null
)