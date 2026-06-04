package com.sunyuanling.filesync.dataClass

import kotlinx.serialization.Serializable

/**
 * 下载请求（简化版）
 */
@Serializable
data class DownloadRequest(
    /** 文件路径 */
    val path: String,
    /** 文件名 */
    val name: String,
    /** 设备ID（可选） */
    val device_id: Int? = null
)

/**
 * 下载响应（已简化，不再需要分片信息）
 */
@Serializable
data class DownloadResponse(
    /** 文件名 */
    val file_name: String,
    /** 文件相对路径 */
    val file_path: String,
    /** 文件大小（字节） */
    val file_size: Long,
    /** MIME类型 */
    val mime_type: String,
    /** 修改时间戳 */
    val mod_time: Long,
    /** 下载历史记录ID */
    val history_id: Int?
)

/**
 * 下载状态
 */
enum class DownloadStatus {
    /** 等待下载 */
    Waiting,
    /** 下载中 */
    Downloading,
    /** 暂停 */
    Paused,
    /** 完成 */
    Completed,
    /** 失败 */
    Failed
}

/**
 * 前端下载列表项（简化版）
 */
data class DownloadItem(
    /** 下载ID（使用 historyId 或生成唯一ID） */
    val downloadId: String,

    /** 文件名 */
    val fileName: String,

    /** 相对路径 */
    val filePath: String,

    /** 本地保存路径 */
    val savePath: String,

    /** 文件总大小（字节） */
    val totalSize: Long,

    /** 已下载字节数 */
    val downloadedSize: Long = 0L,

    /** 下载进度 0–100 */
    val progress: Int = 0,

    /** 当前速度（字节/秒） */
    val speed: Long = 0L,

    /** 下载状态 */
    val status: DownloadStatus = DownloadStatus.Waiting,

    /** MIME 类型 */
    val mimeType: String,

    /** 下载历史记录ID */
    val historyId: Int? = null,

    /** 创建时间 */
    val createTime: Long = System.currentTimeMillis(),

    /** 最近更新时间 */
    val updateTime: Long = System.currentTimeMillis(),

    /** 错误信息（失败时） */
    val errorMessage: String? = null
)