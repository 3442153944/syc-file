package com.sunyuanling.filesync.api.file

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class UploadSpace(
    @SerialName("default_path") val defaultPath: String = "",
    val path: String = "",
    @SerialName("quota_bytes") val quotaBytes: Long = 0,
    @SerialName("used_bytes") val usedBytes: Long = 0,
    @SerialName("expires_at") val expiresAt: String? = null,
    val enabled: Boolean = false,
    val frozen: Boolean = false,
)
