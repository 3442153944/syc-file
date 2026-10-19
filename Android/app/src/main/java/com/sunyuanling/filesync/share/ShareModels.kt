package com.sunyuanling.filesync.share

import kotlinx.serialization.Serializable
import java.net.URI
import java.time.Instant

@Serializable
data class ShareLink(
    val share_code: String = "", val file_name: String = "", val file_size: Long = 0,
    val expire_time: String = "", val created_at: String = "", val status: Int = 1,
    val is_quick_share: Boolean = false, val url_path: String = "",
    val entry_url: String = "", val entry_candidates: List<String> = emptyList()
) {
    val active: Boolean get() = status == 1 && runCatching {
        Instant.parse(expire_time).isAfter(Instant.now())
    }.getOrDefault(false)
    fun url(base: String): String {
        require(share_code.matches(Regex("[A-Za-z0-9_-]+"))) { "分享码无效" }
        // 分享下载统一经过服务端校验，不把存储路径或登录令牌放进链接。
        return entry_url.ifBlank { base }.trimEnd('/') + "/v1/file/share-link/download/" + share_code
    }
}

@Serializable
data class SharePage(val list: List<ShareLink>? = emptyList(), val total: Long = 0)

@Serializable
data class ShareTask(
    val id: String, val name: String, val path: String, val size: Long,
    val owner: Long, val base: String, val realm: String = base, val uploadId: String = "",
    val state: String = "等待", val sent: Long = 0, val error: String = "",
    val result: ShareLink? = null
)

/** 备用入口必须是无凭证的 HTTP(S) 地址，路径保留反代前缀。 */
fun normalizeShareBase(value: String): String {
    val uri = URI(value.trim().trimEnd('/'))
    require(uri.scheme in setOf("http", "https") && !uri.host.isNullOrBlank() &&
        uri.rawUserInfo == null && uri.rawQuery == null && uri.rawFragment == null) { "请填写完整 HTTP(S) 入口，不带凭证、查询或片段" }
    return uri.toASCIIString().trimEnd('/')
}
