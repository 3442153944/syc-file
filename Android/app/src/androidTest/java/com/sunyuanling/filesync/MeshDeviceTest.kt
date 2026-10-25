package com.sunyuanling.filesync

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.sunyuanling.filesync.mesh.MeshController
import com.sunyuanling.filesync.network.NodeFailover
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.util.RootHelper
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

/** Explicit test endpoints and credentials are supplied at invocation; never use production defaults. */
@RunWith(AndroidJUnit4::class)
class MeshDeviceTest {
    @Test fun rootTunnelAndManualNodeSelection() = runBlocking {
        val args = InstrumentationRegistry.getArguments()
        val base = requireNotNull(args.getString("testBase"))
        val password = requireNotNull(args.getString("testPassword"))
        val proxy = requireNotNull(args.getString("testProxy"))
        require(base.startsWith("http://192.168.31.100:8992"))
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        Request.init(context)
        AppConfig.server = "192.168.31.100"; AppConfig.port = 8992; AppConfig.isHttps = false; AppConfig.routePath = ""
        com.sunyuanling.filesync.ui.components.serverSetting.ConfigManager.save()
        val login = buildJsonObject { put("username", "admin"); put("password", password); put("device_id", Request.deviceId()) }
        val request = okhttp3.Request.Builder().url("$base/v1/user/login")
            .post(login.toString().toRequestBody("application/json".toMediaType())).build()
        val token = withContext(Dispatchers.IO) {
            Request.client.newCall(request).execute().use {
                val result = Request.json.parseToJsonElement(it.body.string()).jsonObject
                check(result["code"]?.jsonPrimitive?.int == 200) { "Test login failed" }
                result.getValue("data").jsonObject.getValue("token").jsonPrimitive.content
            }
        }
        Request.saveToken(token)
        withContext(Dispatchers.IO) {
            Request.client.newCall(okhttp3.Request.Builder().url("$proxy/v1/ping").build()).execute().use {
                assertTrue("proxy health HTTP ${it.code}", it.isSuccessful)
                assertTrue(it.body.string().contains("DESKTOP-FOS49RO"))
            }
        }
        NodeFailover.backups = proxy
        NodeFailover.checkNow()
        assertEquals(2, NodeFailover.status.value.nodes.count { it.reachable })
        NodeFailover.enabled = false
        NodeFailover.selectNow(proxy)
        assertEquals(proxy, AppConfig.getBaseUrl())
        NodeFailover.selectNow(base)
        assertEquals(base, AppConfig.getBaseUrl())
        assertTrue("root permission is required by this test", MeshController.useRootBackend())
        val block = "-p tcp -d 192.168.31.100 --dport 8992 -j REJECT"
        try {
            com.sunyuanling.filesync.util.RootHelper.executeRootCommand("iptables -I OUTPUT 1 $block").getOrThrow()
            NodeFailover.enabled = true
            NodeFailover.checkNow()
            assertEquals("failed LAN must fail over to the same test backend via cloud", proxy, AppConfig.getBaseUrl())
        } finally {
            com.sunyuanling.filesync.util.RootHelper.executeRootCommand("iptables -D OUTPUT $block").getOrThrow()
            NodeFailover.selectNow(base)
        }
        // expectClash：Clash 已通过 UI 运行时记录其 VPN handle/tun 接口，断言 Mesh 前/中/后不被抢占。
        val clashBefore = if (args.getString("expectClash") == "true") snapshotClashVpn(context).also {
            assertTrue("expectClash=true 但未发现 Clash VPN 网络，请先通过 UI 启动 Clash", it.handles.isNotEmpty())
            assertTrue("expectClash=true 但未发现 Clash tun 接口", it.tunnelInterfaces.isNotEmpty())
        } else null
        try {
            if (args.getString("testMeshViaProxy") == "true") {
                NodeFailover.selectNow(proxy)
                assertEquals(proxy, AppConfig.getBaseUrl())
            }
            MeshController.connect(context)
            withTimeout(90_000) {
                while (!MeshController.state.value.running || MeshController.state.value.received == 0L) {
                    delay(1_000)
                    val state = MeshController.state.value
                    if (!state.running && !state.busy) error(state.message)
                }
            }
            assertTrue(MeshController.state.value.message.startsWith("Root 独立接口"))
            clashBefore?.let { assertEquals("Mesh 连接期间 Clash VPN 被抢占或 handle 变化", it, snapshotClashVpn(context)) }
            if (args.getString("diagRoute") == "true") {
                // 临时诊断：在 Mesh 已连接时记录 app uid 视角的规则/路由判定。
                for (cmd in listOf(
                    "ip rule show",
                    "ip -4 route show table all dev filesync",
                    "ip route get 172.1.0.254 uid 10053",
                    "ip route get 172.1.0.254",
                    "cat /data/misc/net/rt_tables"
                )) {
                    val result = RootHelper.executeRootCommand(cmd)
                    android.util.Log.i("MeshRouteDiag", "$cmd => " + (result.getOrNull() ?: ("ERR:" + result.exceptionOrNull()?.message)))
                }
            }
            args.getString("holdMeshMs")?.toLongOrNull()?.let { if (it > 0) { android.util.Log.i("MeshRouteDiag", "holding mesh $it ms"); delay(it) } }
            // Clash 等系统 VPN 会向系统写入 HTTP 代理（HttpProxy），OkHttp 默认会走它，
            // 导致发往组网网段的请求被代理转发而不是走内核 WG 路由。组网网段必须 NO_PROXY 直连。
            val directClient = Request.client.newBuilder().proxy(java.net.Proxy.NO_PROXY).build()
            withContext(Dispatchers.IO) {
                directClient.newCall(okhttp3.Request.Builder().url("http://172.1.0.254:18080/probe.txt").build()).execute().use {
                    android.util.Log.i("MeshRouteDiag", "mesh fixture HTTP code=${it.code}")
                    assertEquals("filesync-mesh-fixture", it.body.string().trim())
                }
                args.getString("testPeerUrl")?.let { url ->
                    require(url == "http://172.1.0.4:18082/probe.txt")
                    directClient.newCall(okhttp3.Request.Builder().url(url).build()).execute().use {
                        assertEquals("filesync-windows-lan-fixture", it.body.string().trim())
                    }
                }
            }
        } finally { MeshController.disconnect() }
        assertFalse(MeshController.state.value.running)
        // 断开后 Clash VPN 仍应保持同一 handle 与 tun 接口，证明 Mesh 没有清理掉 Clash。
        clashBefore?.let { assertEquals("断开 Mesh 后 Clash VPN 未保持原状", it, snapshotClashVpn(context)) }
        // 显式 Unit：避免以可空表达式结尾导致 JUnit 认为测试方法返回类型非 void。
        Unit
    }

    /** expectClash 断言用的 Clash VPN 快照：VPN network handle、可见接口名、root 下 tun 接口。 */
    private data class ClashVpnSnapshot(val handles: Set<Long>, val interfaces: Set<String>, val tunnelInterfaces: Set<String>)

    private suspend fun snapshotClashVpn(context: Context): ClashVpnSnapshot {
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val handles = mutableSetOf<Long>()
        val interfaces = mutableSetOf<String>()
        for (network in connectivity.allNetworks) {
            val capabilities = connectivity.getNetworkCapabilities(network) ?: continue
            if (!capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
            handles += network.networkHandle
            runCatching { connectivity.getLinkProperties(network)?.interfaceName }.getOrNull()?.let { interfaces += it }
        }
        // ConnectivityManager 对别的应用 VPN 可能隐藏接口名，root 下用 /sys/class/net 补 Clash tun 接口观测。
        val tunnelInterfaces = RootHelper.executeRootCommand("ls /sys/class/net").getOrNull().orEmpty()
            .lineSequence().map { it.trim() }.filter { it.startsWith("tun") }.toSet()
        return ClashVpnSnapshot(handles, interfaces, tunnelInterfaces)
    }
}
