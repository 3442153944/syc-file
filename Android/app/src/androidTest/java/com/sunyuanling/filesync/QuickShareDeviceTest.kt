package com.sunyuanling.filesync

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.core.NodeSelector
import com.sunyuanling.filesync.core.SyncPlanner
import com.sunyuanling.filesync.network.NodeFailover
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.share.*
import com.sunyuanling.filesync.util.Blake3Util
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.RandomAccessFile
import java.net.ServerSocket
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

@RunWith(AndroidJUnit4::class)
class QuickShareDeviceTest {
    @Test fun rustSelectorAndRealEndpointFailover() = runBlocking {
        val host = AppConfig.server; val port = AppConfig.port; val https = AppConfig.isHttps; val route = AppConfig.routePath
        val backups = NodeFailover.backups; val enabled = NodeFailover.enabled
        Fixture().use { primary -> Fixture().use { backup ->
            try {
                AppConfig.server = "127.0.0.1"; AppConfig.port = primary.port; AppConfig.isHttps = false; AppConfig.routePath = ""
                NodeFailover.enabled = true; NodeFailover.backups = backup.base
                NodeFailover.checkNow()
                assertEquals(primary.base, AppConfig.getBaseUrl())
                primary.close()
                NodeFailover.checkNow()
                assertEquals(backup.base, AppConfig.getBaseUrl())
                val rows = listOf(NodeSelector.Health("old", false, 1, "a"), NodeSelector.Health("other", true, 1, "b"), NodeSelector.Health("same", true, 10, "a"))
                assertEquals("same", NodeSelector.select(NodeSelector.Input("old", true, "a", rows)))
                assertNull(NodeSelector.select(NodeSelector.Input("old", false, "a", rows)))
            } finally {
                AppConfig.server = host; AppConfig.port = port; AppConfig.isHttps = https; AppConfig.routePath = route
                NodeFailover.backups = backups; NodeFailover.enabled = enabled
            }
        } }
    }

    @Test fun rustPlannerUploadsAcrossTwoPathsAndRetriesCompletion() = runBlocking {
        assertTrue("设备必须加载 Rust JNI", SyncPlanner.available)
        assertTrue("请在开发实例先登录", Request.hasToken())
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val file = File(context.cacheDir, "quick-share-multipath-test.bin")
        file.outputStream().use { out -> val block = ByteArray(8192) { (it % 251).toByte() }; repeat(5120) { out.write(block) } }
        val received = ConcurrentHashMap<Int, Boolean>()
        val completeCalls = AtomicInteger()
        Fixture(file, received, completeCalls).use { main ->
            Fixture(file, received, completeCalls).use { second ->
                Fixture(file, received, completeCalls, "other-backend").use { other ->
                    try {
                        val result = withTimeout(90_000) {
                            ChunkedUploader.upload(file, "", ChunkedUploader.UploadOptions(quickShare = true,
                                baseUrl = main.base, deviceId = Request.deviceId(), nodes = listOf(
                                    ChunkedUploader.PlannerNode("active", main.base),
                                    ChunkedUploader.PlannerNode("backup", second.base),
                                    ChunkedUploader.PlannerNode("other", other.base)))).getOrThrow()
                        }
                        assertEquals("fixture-share", result.shareCode)
                        assertTrue("主线路必须实际收到分片", main.chunks.get() > 0)
                        assertTrue("第二线路必须实际收到分片", second.chunks.get() > 0)
                        assertEquals("其他后端不得收到分片", 0, other.chunks.get())
                        assertEquals(10, received.size)
                        assertTrue("完成响应丢失必须重试", completeCalls.get() >= 2)
                        assertTrue(result.entryCandidates.containsAll(listOf(main.base, second.base)))
                    } finally { file.delete() }
                }
            }
        }
    }

    @Test fun realServerQuickShareHistoryAndDownload() = runBlocking {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val owner = QuickShareController.account()
        val api = ShareTransport(AppConfig.getBaseUrl(), Request.getToken() ?: error("请先登录"), Request.deviceId())
        val file = File(context.cacheDir, "android-quick-share-test-${System.currentTimeMillis()}.txt")
        val expected = "Android 快传真实上传校验 ${System.nanoTime()}".toByteArray()
        file.writeBytes(expected)
        var link: ShareLink? = null
        try {
            link = withTimeout(90_000) { api.upload(ShareTask("device-test", file.name, file.path, file.length(), owner, AppConfig.getBaseUrl()), {}, {}) }
            val url = link.url(api.base)
            withContext(Dispatchers.IO) {
                Request.client.newCall(okhttp3.Request.Builder().url(url).build()).execute().use {
                    assertEquals(200, it.code); assertArrayEquals(expected, it.body!!.bytes())
                }
            }
            val page = Request.json.decodeFromJsonElement<SharePage>(api.post("/file/share-link/list", buildJsonObject { put("pageNum", 1); put("pageSize", 20) }))
            assertTrue(page.list.orEmpty().any { it.share_code == link.share_code && it.is_quick_share })
        } finally {
            file.delete()
            link?.let { api.post("/file/share-link/revoke", buildJsonObject { put("share_code", it.share_code) }) }
        }
    }

    /** 两条独立 TCP 入口共享会话位图，逐片核对真实文件内容；第三入口模拟另一个后端。 */
    private class Fixture(val expected: File? = null, val received: ConcurrentHashMap<Int, Boolean> = ConcurrentHashMap(),
        val completes: AtomicInteger = AtomicInteger(), val backend: String = "fixture-backend") : AutoCloseable {
        val server = ServerSocket(0)
        val port = server.localPort
        val base = "http://127.0.0.1:$port"
        val chunks = AtomicInteger()
        private val pool = Executors.newCachedThreadPool()
        init { pool.submit { while (!server.isClosed) runCatching { val socket = server.accept(); pool.submit {
            socket.use {
                socket.soTimeout = 20_000
                val input = socket.getInputStream().buffered()
                fun line(): String {
                    val bytes = java.io.ByteArrayOutputStream()
                    while (true) { val v = input.read(); if (v < 0 || v == 10) break; if (v != 13) bytes.write(v) }
                    return bytes.toString("UTF-8")
                }
                val request = line(); val headers = mutableMapOf<String, String>()
                while (true) { val value = line(); if (value.isEmpty()) break
                    headers[value.substringBefore(':').lowercase()] = value.substringAfter(':').trim() }
                val body = ByteArray(headers["content-length"]?.toIntOrNull() ?: 0)
                var pos = 0; while (pos < body.size) { val n = input.read(body, pos, body.size - pos); if (n < 0) error("请求体不完整"); pos += n }
                val path = request.split(' ')[1]
                val data: String = when {
                    path.contains("/ping") || path.contains("/speedtest/") -> """{"status":"ok","node":"$backend"}"""
                    path.contains("/upload/init") -> {
                        val obj = Request.json.parseToJsonElement(body.decodeToString()).jsonObject
                        check(obj["quick_share"]?.jsonPrimitive?.boolean == true)
                        val count = obj.getValue("chunk_count").jsonPrimitive.int
                        """{"upload_id":"fixture-upload","chunk_size":4194304,"chunk_count":$count,"missing":${(0 until count).toList()}}"""
                    }
                    path.contains("/upload/chunk") -> {
                        val index = path.substringAfter("index=").toInt()
                        if (expected != null) {
                            val actual = ByteArray(body.size)
                            RandomAccessFile(expected, "r").use { raf -> raf.seek(index * 4194304L); raf.readFully(actual) }
                            check(body.contentEquals(actual)) { "分片内容不匹配" }
                        }
                        Thread.sleep(150); chunks.incrementAndGet(); received[index] = true
                        """{"index":$index}"""
                    }
                    path.contains("/upload/complete") -> {
                        if (completes.incrementAndGet() == 1) return@use
                        check(received.size == 10)
                        """{"share_code":"fixture-share","file_name":"fixture.bin","file_size":41943040,"expire_time":"2099-01-01T00:00:00Z","url_path":"/v1/file/share-link/download/fixture-share"}"""
                    }
                    else -> "null"
                }
                val response = """{"code":200,"message":"ok","data":$data}""".toByteArray()
                socket.getOutputStream().apply {
                    write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${response.size}\r\nConnection: close\r\n\r\n".toByteArray()); write(response); flush()
                }
            }
        } } } }
        override fun close() { server.close(); pool.shutdownNow() }
    }
}
