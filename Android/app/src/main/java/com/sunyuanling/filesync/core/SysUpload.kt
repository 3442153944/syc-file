package com.sunyuanling.filesync.core

import android.util.Log
import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.api.file.UploadCompleteData
import com.sunyuanling.filesync.api.file.UploadInitData
import com.sunyuanling.filesync.network.Request
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** JNI event adapter. Rust owns upload execution; Kotlin owns HTTP, lifecycle and preferences. */
object SysUpload {
    private external fun nativeBridgeVersion(): Int
    private external fun nativeStart(input: String): Long
    private external fun nativePoll(job: Long): String
    private external fun nativeTakeBody(job: Long, request: Long): ByteArray
    private external fun nativeReply(job: Long, request: Long, result: String)
    private external fun nativeCancel(job: Long)

    suspend fun upload(file: File, remoteDir: String, options: ChunkedUploader.UploadOptions,
        progress: (Long, Long) -> Unit): UploadCompleteData = withContext(Dispatchers.IO) {
        check(SyncCore.available && runCatching { nativeBridgeVersion() == 1 }.getOrDefault(false)) {
            "sys_core 上传内核不可用，请重新构建 Android 原生库"
        }
        val token = Request.getToken() ?: error("请先登录")
        val nodes = options.nodes ?: ChunkedUploader.buildNodePool(options.baseUrl)
        val input = buildJsonObject {
            put("file", file.absolutePath); put("remote_dir", remoteDir); put("base_url", options.baseUrl)
            put("device_id", options.deviceId); put("quick_share", options.quickShare)
            put("chunk_size", options.chunkSize?.let(::JsonPrimitive) ?: JsonNull)
            put("concurrency", options.concurrency?.let(::JsonPrimitive) ?: JsonNull)
            put("main_node", nodes.firstOrNull { it.url.trimEnd('/') == options.baseUrl.trimEnd('/') }?.id ?: "active")
            put("nodes", JsonArray(nodes.map { n -> buildJsonObject {
                put("id", n.id); put("url", n.url); n.parent?.let { put("parent", it) }
            } }))
            SyncPlanner.loadState()?.let { state ->
                runCatching { Request.json.parseToJsonElement(state) }.getOrNull()?.let { put("state", it) }
            }
            put("resume", options.resume?.let { Request.json.encodeToJsonElement(UploadInitData.serializer(), it) } ?: JsonNull)
        }
        val job = nativeStart(input.toString())
        check(job != 0L) { "sys_core 上传任务创建失败" }
        val calls = mutableMapOf<Long, Job>()
        try {
            coroutineScope {
                while (true) {
                    ensureActive()
                    val events = Request.json.parseToJsonElement(nativePoll(job)).jsonArray
                    for (item in events) {
                        val event = item.jsonObject
                        when (event.getValue("type").jsonPrimitive.content) {
                            "request", "probe_share" -> {
                                val id = event.getValue("id").jsonPrimitive.long
                                calls[id] = launch {
                                    val result = try {
                                        val response = if (event.getValue("type").jsonPrimitive.content == "probe_share") {
                                            probe(event.getValue("url").jsonPrimitive.content)
                                            buildJsonObject { put("code", 200); put("message", ""); put("data", JsonNull) }
                                        } else request(job, id, event, token, options.deviceId)
                                        buildJsonObject { put("response", response) }
                                    } catch (ce: CancellationException) { throw ce }
                                      catch (e: Exception) { buildJsonObject { put("error", e.message ?: "HTTP 请求失败") } }
                                    nativeReply(job, id, result.toString())
                                }
                            }
                            "cancel_request" -> calls.remove(event.getValue("id").jsonPrimitive.long)?.cancel()
                            "progress" -> progress(event.getValue("sent").jsonPrimitive.long, event.getValue("total").jsonPrimitive.long)
                            "session" -> options.onSession(event.getValue("upload_id").jsonPrimitive.content)
                            "state" -> SyncPlanner.saveState(event.getValue("state").toString())
                            "log" -> {
                                val message = event.getValue("message").jsonPrimitive.content
                                if (event.getValue("level").jsonPrimitive.content == "warn") Log.w("SysUpload", message)
                                else Log.i("SysUpload", message)
                            }
                            "done" -> { calls.values.forEach { it.cancel() }; return@coroutineScope Request.json.decodeFromJsonElement<UploadCompleteData>(event.getValue("data")) }
                            "failed" -> error(event.getValue("error").jsonPrimitive.content)
                        }
                    }
                    calls.entries.removeAll { it.value.isCompleted }
                    if (events.isEmpty()) delay(10)
                }
                @Suppress("UNREACHABLE_CODE") error("上传循环已退出")
            }
        } finally {
            calls.values.forEach { it.cancel() }
            nativeCancel(job)
        }
    }

    private suspend fun request(job: Long, id: Long, event: JsonObject, token: String, device: String): JsonObject {
        val url = (event.getValue("base_url").jsonPrimitive.content.trimEnd('/') + "/v1" + event.getValue("path").jsonPrimitive.content).toHttpUrl().newBuilder()
        event.getValue("query").jsonArray.forEach { pair -> url.addQueryParameter(pair.jsonArray[0].jsonPrimitive.content, pair.jsonArray[1].jsonPrimitive.content) }
        val body = event.getValue("body").jsonObject
        val data = if (body.getValue("kind").jsonPrimitive.content == "bytes")
            nativeTakeBody(job, id).toRequestBody("application/octet-stream".toMediaType())
            else body.getValue("value").toString().toRequestBody("application/json".toMediaType())
        val request = okhttp3.Request.Builder().url(url.build()).header("Token", token).header("Device-Id", device).post(data).build()
        val text = send(request, event.getValue("timeout_ms").jsonPrimitive.long)
        val response = try { Request.json.parseToJsonElement(text).jsonObject }
            catch (e: Exception) { throw IOException("JSON 解析失败: ${e.message}", e) }
        Request.handleBusinessUnauthorized(response["code"]?.jsonPrimitive?.intOrNull)
        return response
    }

    private suspend fun probe(url: String) {
        val request = okhttp3.Request.Builder().url(url).header("Range", "bytes=0-0").get().build()
        send(request, 10_000, probe = true)
    }
    /** Cancellation remains attached until the entire response body has been consumed. */
    private suspend fun send(request: okhttp3.Request, timeoutMs: Long, probe: Boolean = false): String = suspendCancellableCoroutine { continuation ->
        val client = Request.client.newBuilder().retryOnConnectionFailure(false).followRedirects(false)
            .protocols(listOf(Protocol.HTTP_1_1)).callTimeout(timeoutMs, TimeUnit.MILLISECONDS).build()
        val call = client.newCall(request)
        continuation.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                if (continuation.isActive) continuation.resumeWithException(e)
            }
            override fun onResponse(call: Call, response: Response) {
                response.use {
                    try {
                        if (!it.isSuccessful) throw IOException("HTTP ${it.code}")
                        val text = if (probe) {
                            check(it.code == 206 && it.header("Content-Disposition")?.contains("attachment", true) == true &&
                                it.header("Content-Range")?.startsWith("bytes 0-0/") == true && it.body?.byteStream()?.read()?.let { byte -> byte >= 0 } == true) { "分享入口不可下载" }
                            ""
                        } else it.body?.string() ?: throw IOException("响应为空")
                        if (continuation.isActive) continuation.resume(text)
                    } catch (e: Exception) {
                        if (continuation.isActive) continuation.resumeWithException(e)
                    }
                }
            }
        })
    }
}
