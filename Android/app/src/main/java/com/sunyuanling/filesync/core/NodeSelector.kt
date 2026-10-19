package com.sunyuanling.filesync.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

/** 仅封送数据；入口保留、故障切换和延迟排序均由共享 Rust 核心决定。 */
object NodeSelector {
    @Serializable
    data class Health(val id: String, val reachable: Boolean, val latency_ms: Long, val backend: String = "")
    @Serializable
    data class Input(val active: String, val auto_failover: Boolean, val required_backend: String?, val nodes: List<Health>)
    private external fun nativeSelect(input: String): String?
    fun select(input: Input): String? {
        check(SyncCore.available) { "Rust 核心不可用，无法自动选择节点" }
        val value = nativeSelect(Json.encodeToString(input)) ?: error("Rust 节点选择失败")
        return Json.decodeFromString<String?>(value)
    }
}
