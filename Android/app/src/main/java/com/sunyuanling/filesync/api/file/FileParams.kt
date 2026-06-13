// api/file/FileParams.kt
// 职责：文件模块所有请求参数数据类。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag。
package com.sunyuanling.filesync.api.file

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

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

// ==================== 上传 ====================

/**
 * 上传请求参数。
 * JSON 模式：用于 check 操作，path/name/action 作为 JSON body 发送。
 * Multipart 模式：用于 upload 操作，path/name/action 作为 form 字段，文件作为 multipart 字段。
 *
 * TODO: upload 操作需构造 MultipartBody，当前 Request 单例不支持，
 * 调用方需自行构建 OkHttp MultipartBody 请求。
 */
@Serializable
data class UploadParams(
    /** 目标路径（必填） */
    val path: String,
    /** 文件名（必填） */
    val name: String,
    /** 操作类型：check（检查文件是否存在）/ upload（上传文件） */
    val action: String
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
