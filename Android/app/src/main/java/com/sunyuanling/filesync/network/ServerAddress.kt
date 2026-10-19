package com.sunyuanling.filesync.network

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

object ServerAddress {
    fun base(host: String, port: Int, https: Boolean, route: String): String {
        val address = host.trim().let { if (it.contains(':') && !it.startsWith('[')) "[$it]" else it }
        val prefix = route.trim().trim('/').let { if (it.isEmpty()) "" else "/$it" }
        return "${if (https) "https" else "http"}://$address:$port$prefix"
    }

    // 既检查 HTTP 状态，也确认响应来自 FileSync 健康检查接口。
    fun healthy(status: Int, body: String): Boolean = status == 200 && runCatching {
        val value = Json.parseToJsonElement(body).jsonObject
        value["code"]?.jsonPrimitive?.intOrNull == 200 &&
            value["data"]?.jsonObject?.get("status")?.jsonPrimitive?.contentOrNull == "ok"
    }.getOrDefault(false)
}
