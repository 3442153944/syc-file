package com.sunyuanling.filesync.dataClass

import kotlinx.serialization.Serializable

// @Serializable 数据类已迁移至 api/file/FileParams.kt（DownloadParams）和 api/file/FileResponse.kt。
// 本文件仅保留前端 UI 模型（非序列化）。

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
@Serializable
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

    /** 远端设备 ID（用于下载 URL 的 device_id，重试时复用，修原 historyId 误用） */
    val deviceId: String? = null,

    /** 下载历史记录ID */
    val historyId: Int? = null,

    /** 创建时间 */
    val createTime: Long = System.currentTimeMillis(),

    /** 最近更新时间 */
    val updateTime: Long = System.currentTimeMillis(),

    /** 错误信息（失败时） */
    val errorMessage: String? = null
)