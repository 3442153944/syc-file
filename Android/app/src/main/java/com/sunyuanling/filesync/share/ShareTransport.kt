package com.sunyuanling.filesync.share

import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.awaitResponse
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

class ShareApiException(val code: Int, message: String) : IOException(message)

/** 每个任务固定入口和登录身份，灾备切换不会更改正在上传的会话。 */
class ShareTransport(val base: String, private val token: String, private val device: String) {
    private val client = Request.client.newBuilder().retryOnConnectionFailure(false)
        .protocols(listOf(Protocol.HTTP_1_1)).followRedirects(false)
        .callTimeout(120, TimeUnit.SECONDS).build()

    suspend fun post(path: String, body: JsonObject = buildJsonObject {}): JsonElement =
        send(path, body.toString().toRequestBody("application/json".toMediaType()))

    private suspend fun send(path: String, body: RequestBody?): JsonElement {
        val request = okhttp3.Request.Builder().url(base.trimEnd('/') + "/v1" + path)
            .header("Token", token).header("Device-Id", device).apply { if (body == null) get() else post(body) }.build()
        val text = suspendCancellableCoroutine<String> { continuation ->
            val call = client.newCall(request)
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, e: IOException) {
                    if (continuation.isActive) continuation.resumeWithException(e)
                }
                override fun onResponse(call: Call, response: Response) {
                    response.use {
                        try {
                            if (!it.isSuccessful) throw ShareApiException(it.code, "HTTP ${it.code}")
                            val value = it.body?.string() ?: throw IOException("响应为空")
                            if (continuation.isActive) continuation.resume(value)
                        } catch (e: Exception) {
                            if (continuation.isActive) continuation.resumeWithException(e)
                        }
                    }
                }
            })
        }
        val obj = Request.json.parseToJsonElement(text).jsonObject
        val code = obj["code"]?.jsonPrimitive?.intOrNull ?: -1
        if (code != 200) throw ShareApiException(code, obj["message"]?.jsonPrimitive?.content ?: "快传请求失败")
        return obj["data"] ?: JsonNull
    }

    suspend fun chunk(id: String, index: Int, bytes: ByteArray) {
        send("/file/upload/chunk?upload_id=$id&index=$index", bytes.toRequestBody("application/octet-stream".toMediaType()))
    }

    suspend fun completeData(id: String, candidates: List<String> = listOf(base)): JsonElement {
        val entries = candidates.distinct().sortedBy { !it.startsWith("https://") }
        require(entries.isNotEmpty()) { "没有同后端的可用上传收尾节点" }
        var error: Exception = IOException("上传收尾节点均失败")
        repeat(3) {
            for (entry in entries) {
                try {
                    val data = ShareTransport(entry, token, device).post("/file/upload/complete",
                        buildJsonObject { put("upload_id", id); put("device_id", device) }).jsonObject
                    return JsonObject(data + mapOf("entry_url" to JsonPrimitive(entry),
                        "entry_candidates" to JsonArray(entries.map(::JsonPrimitive))))
                } catch (e: IOException) {
                    if (e is ShareApiException && e.code in setOf(401, 403, 404, 422)) throw e
                    error = e
                }
            }
            delay(300)
        }
        throw error
    }

    suspend fun upload(task: ShareTask, session: suspend (String) -> Unit, progress: (Long) -> Unit): ShareLink {
        val file = File(task.path)
        require(file.isFile && file.length() == task.size && task.size > 0) { "暂存文件不存在、为空或大小已改变" }
        var resume: com.sunyuanling.filesync.api.file.UploadInitData? = null
        if (task.uploadId.isNotBlank()) {
            try {
                return verified(Request.json.decodeFromJsonElement(completeData(task.uploadId)))
            } catch (e: ShareApiException) {
                if (e.code != 404 && e.code != 400) throw e
                if (e.code == 400) {
                    val status = getStatus(task.uploadId).jsonObject
                    resume = com.sunyuanling.filesync.api.file.UploadInitData(
                        uploadId = task.uploadId,
                        chunkSize = status.getValue("chunk_size").jsonPrimitive.long,
                        chunkCount = status.getValue("chunk_count").jsonPrimitive.int,
                        missing = status.getValue("missing").jsonArray.map { it.jsonPrimitive.int })
                }
            }
        }
        // 普通上传与快传走同一 JNI 规划器，保留多节点、多线路和父链瓶颈调度。
        val result = ChunkedUploader.upload(file, "", ChunkedUploader.UploadOptions(
            deviceId = device, quickShare = true, baseUrl = base, resume = resume, onSession = session
        )) { sent, _ -> progress(sent) }.getOrThrow()
        return verified(ShareLink(share_code = result.shareCode, file_name = result.fileName,
            file_size = result.fileSize, expire_time = result.expireTime, is_quick_share = true,
            url_path = result.urlPath, entry_url = result.entryUrl, entry_candidates = result.entryCandidates))
    }

    private suspend fun verified(link: ShareLink): ShareLink = withContext(Dispatchers.IO) {
        val entries = link.entry_candidates.ifEmpty { listOf(base) }.distinct().sortedBy { !it.startsWith("https://") }
        for (entry in entries) {
            currentCoroutineContext().ensureActive()
            val url = link.copy(entry_url = entry).url(entry)
            val valid = runCatching {
                // 与 Windows 一样，以真实 Range 下载与附件头验证入口，禁止复制 404 或 HTML 页面。
                client.newCall(okhttp3.Request.Builder().url(url).header("Range", "bytes=0-0").build()).awaitResponse().use {
                    it.code == 206 && it.header("Content-Disposition")?.contains("attachment", true) == true &&
                        it.header("Content-Range")?.startsWith("bytes 0-0/") == true && it.body?.byteStream()?.read() != -1
                }
            }.getOrDefault(false)
            if (valid) return@withContext link.copy(entry_url = entry, entry_candidates = entries)
        }
        error("文件已完成，但分享入口不可下载；未复制失效链接，可在分享管理中重试")
    }

    private suspend fun getStatus(id: String): JsonElement = send(
        "/file/upload/status?upload_id=$id", null)
    private suspend fun <T> retrySafe(block: suspend () -> T): T {
        repeat(2) { attempt ->
            try { return block() } catch (e: IOException) {
                if (e is ShareApiException && e.code !in setOf(429, 500, 502, 503, 504)) throw e
                delay(500L * (attempt + 1))
            }
        }
        return block()
    }
}


