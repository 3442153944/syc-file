// api/file/FileParams.kt
// 职责：文件模块所有请求参数数据类。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag。
package com.sunyuanling.filesync.api.file

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// ==================== 删除文件 ====================

/** 删除远端单个文件（同步 delete-before-upload 覆盖旧文件用，拒绝目录）。 */
@Serializable
data class DeleteFileParams(
    /** 远端目录 */
    val path: String,
    /** 文件名 */
    val name: String
)

// ==================== 获取可用磁盘 ====================

@Serializable
data class AvailableDisksParams(
    /** 指定磁盘路径（可选，为空则返回所有磁盘列表） */
    @SerialName("disk_path")
    val diskPath: String = "",
    /** 是否返回详细信息（可选） */
    val detailed: Boolean = false
)

// ==================== 遍历目录 ====================

@Serializable
data class TraverseDirectoryParams(
    /** 目录路径（必填） */
    val path: String,
    /** 页码（选填，不传则返回全部） */
    val page: Int = 0,
    /** 每页数量（选填） */
    @SerialName("page_size")
    val pageSize: Int = 0
)

// ==================== 下载 ====================

/**
 * 下载请求参数。
 * 注意：/v1/file/download 是 GET 请求，参数以 query string 形式传递。
 * token 无法通过 header 携带，需拼入 URL query 参数。
 * 详见 FileApi.buildDownloadUrl()。
 */
data class DownloadParams(
    /** 文件路径（必填） */
    val path: String,
    /** 文件名（必填） */
    val name: String,
    /** 设备 ID（可选） */
    val deviceId: String = ""
)

// ==================== 分片上传 ====================

/**
 * 分片上传初始化描述信息。所有哈希均为 blake3 hex（与服务端 file_lib 一致）。
 */
@Serializable
data class UploadInitParams(
    /** 目标目录（必填） */
    val path: String,
    /** 文件名（必填） */
    val name: String,
    @SerialName("total_size") val totalSize: Long,
    @SerialName("chunk_size") val chunkSize: Long,
    @SerialName("chunk_count") val chunkCount: Int,
    /** blake3 Merkle 树根 hex */
    @SerialName("merkle_root") val merkleRoot: String,
    /** 整文件 blake3 hex（秒传/去重键） */
    @SerialName("file_hash") val fileHash: String,
    /** 每片叶子哈希 hex，长度须等于 chunk_count */
    @SerialName("leaf_hashes") val leafHashes: List<String>,
    /** 本机设备 id：秒传在 init 阶段直接完成，服务端同步派发需要排除源设备 */
    @SerialName("device_id") val deviceId: String = ""
)

/**
 * 分片上传完成。
 */
@Serializable
data class UploadCompleteParams(
    @SerialName("upload_id") val uploadId: String,
    /** 本机设备 id，服务端派发同步任务时排除源设备 */
    @SerialName("device_id") val deviceId: String = ""
)

// ==================== 下载历史 ====================

@Serializable
data class DownloadHistoryParams(
    /** 页码 */
    val pageNum: Int = 1,
    /** 每页数量 */
    val pageSize: Int = 10
)

@Serializable
/** 删除下载记录ids */
data class DeleteDownloadHistoryParams(
    /** 下载记录 ids 列表 */
    val ids: List<Int>
)
