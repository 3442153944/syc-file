// api/update/UpdateModels.kt
// 应用更新相关 DTO。字段对齐后端 internal/model/AppRelease.go + update/handler.go 的 Check 返回。
package com.sunyuanling.filesync.api.update

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** 一个发布版本。file_path/file_name 用于复用 /v1/file/download 下载 APK；file_hash 为 blake3 hex 校验用。 */
@Serializable
data class AppReleaseDto(
    val id: Long = 0,
    val platform: String = "",
    @SerialName("version_code") val versionCode: Long = 0,
    @SerialName("version_name") val versionName: String = "",
    val notes: String = "",
    @SerialName("file_path") val filePath: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_hash") val fileHash: String = "",
    val mandatory: Boolean = false,
    @SerialName("min_version_code") val minVersionCode: Long = 0,
    val enabled: Boolean = true
)

/** /update/check 的 data：是否有更新 + 是否强制 + 版本详情（无更新时 release 可能仍带最新版）。 */
@Serializable
data class CheckResult(
    @SerialName("has_update") val hasUpdate: Boolean = false,
    val mandatory: Boolean = false,
    val release: AppReleaseDto? = null
)
