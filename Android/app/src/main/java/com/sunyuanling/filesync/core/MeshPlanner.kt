package com.sunyuanling.filesync.core

import kotlinx.serialization.json.*

/** LAN / 公网探测轮转和中继路由由共享 Rust 内核计算。 */
object MeshPlanner {
    private external fun nativePlan(input: String): String?
    fun plan(peers: JsonArray, round: Long, localNetworks: List<String> = emptyList()): JsonArray {
        check(SyncCore.available) { "Rust 核心不可用" }
        val input = buildJsonObject { put("peers", peers); put("round", round); put("local_networks", JsonArray(localNetworks.map(::JsonPrimitive))) }
        val output = nativePlan(input.toString()) ?: error("组网路由规划失败")
        return Json.parseToJsonElement(output).jsonArray
    }
}
