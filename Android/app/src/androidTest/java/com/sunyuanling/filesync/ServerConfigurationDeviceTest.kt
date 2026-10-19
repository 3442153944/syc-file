package com.sunyuanling.filesync

import androidx.test.ext.junit.runners.AndroidJUnit4
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.net.ServerSocket
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class ServerConfigurationDeviceTest {
    @Test fun requestsUseNewServerWithoutRestartingRequestSingleton() = runBlocking {
        val previousHost = AppConfig.server
        val previousPort = AppConfig.port
        val previousHttps = AppConfig.isHttps
        val previousRoute = AppConfig.routePath
        val executor = Executors.newSingleThreadExecutor()
        try {
            // 先初始化请求单例，再连续切换端口和路径，复现保存设置后立即请求的场景。
            Request.baseUrl
            repeat(2) { index ->
                ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { listener ->
                    listener.soTimeout = 5000
                    val received = executor.submit<String> {
                        listener.accept().use { socket ->
                            socket.soTimeout = 5000
                            val reader = socket.getInputStream().bufferedReader()
                            val first = requireNotNull(reader.readLine())
                            while (!reader.readLine().isNullOrEmpty()) { }
                            val body = """{"code":200,"message":"ok","data":null}""".toByteArray()
                            socket.getOutputStream().apply {
                                write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${body.size}\r\nConnection: close\r\n\r\n".toByteArray())
                                write(body)
                                flush()
                            }
                            first
                        }
                    }
                    val host = if (index == 0) "127.0.0.1" else "localhost"
                    AppConfig.server = host
                    AppConfig.port = listener.localPort
                    AppConfig.isHttps = false
                    AppConfig.routePath = if (index == 0) "first/" else "/second"
                    val prefix = if (index == 0) "first" else "second"
                    val response = Request.getSuspend<Response<Unit?>>("/ping")
                    assertTrue(response.exceptionOrNull()?.message, response.isSuccess)
                    assertEquals("GET /$prefix/v1/ping HTTP/1.1", received.get(5, TimeUnit.SECONDS))
                    assertEquals("http://$host:${listener.localPort}/$prefix", Request.baseStaticUrl)
                    assertEquals("ws://$host:${listener.localPort}/$prefix/v1/ws/connect", AppConfig.getWsUrl())
                }
            }
        } finally {
            AppConfig.server = previousHost
            AppConfig.port = previousPort
            AppConfig.isHttps = previousHttps
            AppConfig.routePath = previousRoute
            executor.shutdownNow()
        }
    }
}
