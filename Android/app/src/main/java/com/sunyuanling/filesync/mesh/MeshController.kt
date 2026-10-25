package com.sunyuanling.filesync.mesh

import android.content.Context
import android.util.Log
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.network.Request
import com.wireguard.android.backend.GoBackend
import com.wireguard.android.backend.Backend
import com.wireguard.android.backend.WgQuickBackend
import com.wireguard.android.backend.Tunnel
import com.wireguard.config.Config
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.ByteArrayInputStream
import java.io.IOException
import java.net.NetworkInterface
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/** 4xx 客户端错误（408 超时、429 限流除外）重试也不会成功，按致命错误处理。 */
private fun isFatalStatus(status: Int): Boolean = status in 400..499 && status != 408 && status != 429

/** Android 仅负责认证 HTTP、系统 VPN 和隧道生命周期，密钥只在当前连接内存中。 */
object MeshController {
    data class State(val running: Boolean = false, val busy: Boolean = false, val address: String = "",
        val message: String = "组网未连接", val received: Long = 0, val sent: Long = 0)
    private val mutableState = MutableStateFlow(State())
    val state = mutableState.asStateFlow()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val mutex = Mutex()
    private var backend: Backend? = null
    private var rootShell: com.wireguard.android.util.RootShell? = null
    private var session: Job? = null
    /** root 专用接口的 172.1.0.0/16 优先规则是否由本次会话添加（断开时清理）。 */
    private var routeRuleAdded = false
    /** 连接代际：disconnect/新连接会推进，旧 session 的收尾不得覆盖新 session 的状态。 */
    private val epoch = AtomicLong(0)
    private val tunnel = object : Tunnel {
        override fun getName() = "filesync"
        // WgQuickBackend briefly reports DOWN while applying a changed config.
        // Only the session loop/finally owns public connection state; otherwise
        // periodic endpoint rotation looks like a user-visible disconnection.
        override fun onStateChange(value: Tunnel.State) = Unit
    }

    /** 组网接口错误：httpStatus 是 HTTP 状态码，bizCode 是响应体里的业务码，任一 4xx 即致命。 */
    private class ApiException(val httpStatus: Int, val bizCode: Int?, message: String) : Exception(message) {
        val fatal: Boolean get() = isFatalStatus(httpStatus) || (bizCode != null && isFatalStatus(bizCode))
    }

    /** 本地判定必须停止组网并清理隧道的错误（登录身份/服务器/公钥变化、系统撤销 VPN 等）。 */
    private class FatalMeshException(message: String) : Exception(message)

    /** 只有网络/服务端临时问题可重试；鉴权、本地状态和响应契约问题按致命错误停止。 */
    private fun isTransient(error: Throwable): Boolean = when (error) {
        is ApiException -> !error.fatal
        is IOException -> true
        else -> false
    }

    /** 2s→4s→8s→16s→30s，封顶 30s。 */
    private fun retryDelayMillis(failures: Int): Long = minOf(2_000L shl minOf(failures - 1, 4), 30_000L)

    /**
     * 组网 HTTP 调用。用 enqueue + invokeOnCancellation { call.cancel() }：协程取消（断开组网）时立即
     * 中断连接和响应体读取，不再像 execute()+body.string() 那样阻塞到 30 秒超时。
     * 取消后回调里的 resumeWith 会被 CancellableContinuation 忽略；completed 只防 onFailure/onResponse 双回调。
     */
    private suspend fun api(base: String, path: String, body: JsonObject? = null): JsonObject {
        val token = Request.getToken()?.takeIf { it.isNotBlank() } ?: throw FatalMeshException("请先登录")
        val request = okhttp3.Request.Builder().url("$base/v1/vpn/$path")
            .header("Token", token).header("Device-Id", Request.deviceId())
        if (body != null) request.post(body.toString().toRequestBody("application/json".toMediaType()))
        val call = Request.client.newCall(request.build())
        return suspendCancellableCoroutine { continuation ->
            val completed = AtomicBoolean(false)
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, e: IOException) {
                    if (completed.compareAndSet(false, true)) continuation.resumeWith(Result.failure(e))
                }

                override fun onResponse(call: Call, response: Response) {
                    val outcome = runCatching { parseResponse(response) }
                    if (completed.compareAndSet(false, true)) continuation.resumeWith(outcome)
                }
            })
        }
    }

    /** 解析响应：HTTP/业务码错误转成 ApiException，供会话循环区分瞬时与致命。 */
    private fun parseResponse(response: Response): JsonObject = response.use { resp ->
        val text = resp.body.string()
        if (!resp.isSuccessful) throw ApiException(resp.code, null, "组网请求失败 HTTP ${resp.code}")
        val result = try {
            Request.json.parseToJsonElement(text).jsonObject
        } catch (e: Exception) {
            // 网关错误页或被中断的响应可能不是 JSON，按瞬时错误重试。
            throw ApiException(resp.code, null, "组网响应不是合法 JSON")
        }
        val code = result["code"]?.jsonPrimitive?.intOrNull
        if (code != 200) throw ApiException(resp.code, code, "组网请求失败 ($code)：${result["message"]?.jsonPrimitive?.content.orEmpty()}")
        result["data"]?.jsonObject ?: throw FatalMeshException("组网响应缺少 data 字段")
    }

    fun connect(context: Context) {
        if (session?.isActive == true) return
        val generation = epoch.incrementAndGet()
        session = scope.launch { mutex.withLock { runSession(context.applicationContext, generation) } }
    }

    private suspend fun CoroutineScope.runSession(context: Context, generation: Long) {
        if (epoch.get() != generation) return
        mutableState.value = State(busy = true, message = "正在登记组网设备")
        try {
            val base = AppConfig.getBaseUrl()
            val realm = AppConfig.getConfiguredBaseUrl()
            val accountToken = Request.getToken()
            val pair = com.wireguard.crypto.KeyPair()
            val identity = api(base, "auto-enroll", buildJsonObject { put("pubkey", pair.publicKey.toBase64()) })
            val device = Request.deviceId()
            val key = pair.privateKey.toBase64()
            val address = identity.getValue("address").jsonPrimitive.content
            val engine = backend ?: createBackend(context).also { backend = it }
            var configuration = ""
            var failures = 0
            var round = 0L
            data class Attempt(val endpoint: String, val since: Long)
            var configuredPeers = emptyMap<String, Attempt>()
            while (isActive) {
                if (epoch.get() != generation) return
                try {
                    // 登录身份/服务器/公钥变化是致命错误：停止并清理隧道，交给用户重新连接。
                    if (Request.getToken() != accountToken || AppConfig.getConfiguredBaseUrl() != realm) {
                        throw FatalMeshException("登录身份或服务器已变化，请重新连接组网")
                    }
                    val peers = api(AppConfig.getBaseUrl(), "peers?device_id=$device")
                    if (peers.getValue("self").jsonObject.getValue("pubkey").jsonPrimitive.content != pair.publicKey.toBase64()) {
                        throw FatalMeshException("服务端登记的公钥与当前设备不一致，请重新连接组网")
                    }
                    val statistics = metrics(engine)
                    val observed = JsonArray(peers.getValue("peers").jsonArray.map { item ->
                        val peer = item.jsonObject
                        val pubkey = peer.getValue("pubkey").jsonPrimitive.content
                        val lastHandshake = statistics[pubkey]?.handshake ?: 0
                        val attempt = configuredPeers[pubkey]
                        if (attempt != null && lastHandshake > 0 && lastHandshake >= attempt.since && System.currentTimeMillis() - lastHandshake < 180_000)
                            JsonObject(peer + ("confirmed_endpoint" to JsonPrimitive(attempt.endpoint))) else peer
                    })
                    val routes = com.sunyuanling.filesync.core.MeshPlanner.plan(observed, round++, localNetworks())
                    val conf = buildString {
                        append("[Interface]\nPrivateKey = $key\nAddress = $address/32\nListenPort = 51821\nMTU = 1280\n")
                        routes.forEach { item ->
                            val peer = item.jsonObject
                            val endpoint = peer["endpoint"]?.jsonPrimitive?.content.orEmpty()
                            // 没有端点的对端暂不设置独占路由，避免黑洞。
                            if (endpoint.isNotBlank()) {
                                append("\n[Peer]\nPublicKey = ${peer.getValue("pubkey").jsonPrimitive.content}\n")
                                val prefixes = peer.getValue("allowed_ips").jsonArray
                                if (prefixes.isNotEmpty()) append("AllowedIPs = ${prefixes.joinToString(", ") { it.jsonPrimitive.content }}\n")
                                append("Endpoint = $endpoint\nPersistentKeepalive = 5\n")
                            }
                        }
                    }
                    if (configuration != conf) {
                        val since = System.currentTimeMillis() / 1000 * 1000
                        engine.setState(tunnel, Tunnel.State.UP, Config.parse(ByteArrayInputStream(conf.toByteArray())))
                        configuration = conf
                        configuredPeers = routes.associate {
                            val p = it.jsonObject
                            val pubkey = p.getValue("pubkey").jsonPrimitive.content
                            val endpoint = p.getValue("endpoint").jsonPrimitive.content
                            pubkey to (configuredPeers[pubkey]?.takeIf { old -> old.endpoint == endpoint } ?: Attempt(endpoint, since))
                        }
                        // Clash 等系统 VPN 会抢占 app 默认网络；root 独立接口需给组网网段加一条优先 lookup。
                        if (engine is WgQuickBackend) ensureRouteRule()
                    }
                    val stats = metrics(engine)
                    val handshakes = stats.values.count { it.handshake > 0 && System.currentTimeMillis() - it.handshake < 180_000 }
                    mutableState.value = State(true, false, address,
                        "${if (engine is WgQuickBackend) "Root 独立接口" else "系统 VPN"}运行，最近握手 $handshakes 个对端", stats.values.sumOf { it.rx }, stats.values.sumOf { it.tx })
                    api(AppConfig.getBaseUrl(), "heartbeat", buildJsonObject { put("device_id", device); put("endpoint", "");
                        put("lan_endpoints", JsonArray(lanEndpoints().map(::JsonPrimitive))) })
                    failures = 0
                    delay(15_000)
                    if (engine.getState(tunnel) != Tunnel.State.UP) throw FatalMeshException("系统已撤销 VPN")
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    if (!isTransient(e)) throw e
                    // 瞬时错误只提示并退避重试，保持已建立的 WG 接口，避免一次网络抖动就拆掉隧道。
                    failures++
                    val waitMillis = retryDelayMillis(failures)
                    mutableState.value = mutableState.value.copy(message = "网络波动，重试中（${waitMillis / 1000} 秒后重试）")
                    delay(waitMillis)
                }
            }
        } catch (e: CancellationException) { throw e
        } catch (e: Exception) { mutableState.value = mutableState.value.copy(message = e.message ?: "组网失败")
        } finally {
            withContext(NonCancellable) {
                clearRouteRule()
                runCatching { backend?.setState(tunnel, Tunnel.State.DOWN, null) }
                rootShell?.stop()
                rootShell = null
                backend = null
                // 旧代际只释放自己的资源；状态只在仍是当前代际时回写，避免覆盖新 session。
                if (epoch.get() == generation) mutableState.value = mutableState.value.copy(running = false, busy = false)
            }
        }
    }

    suspend fun useRootBackend(): Boolean = com.sunyuanling.filesync.util.RootHelper.checkRootAccess()

    private data class Metrics(val handshake: Long = 0, val rx: Long = 0, val tx: Long = 0)
    private fun metrics(engine: Backend): Map<String, Metrics> {
        if (engine.getState(tunnel) != Tunnel.State.UP) return emptyMap()
        if (engine !is WgQuickBackend) {
            val stats = engine.getStatistics(tunnel)
            return stats.peers().associate { key -> val p = stats.peer(key)
                key.toBase64() to Metrics(p?.latestHandshakeEpochMillis ?: 0, p?.rxBytes ?: 0, p?.txBytes ?: 0) }
        }
        // RootShell logs command output; never use `wg ... dump`, which includes the private key.
        val shell = checkNotNull(rootShell)
        val handshakes = mutableListOf<String>()
        val transfers = mutableListOf<String>()
        check(shell.run(handshakes, "wg show filesync latest-handshakes") == 0) { "读取隧道握手失败" }
        check(shell.run(transfers, "wg show filesync transfer") == 0) { "读取隧道流量失败" }
        val values = handshakes.mapNotNull { val fields = it.trim().split(Regex("\\s+"))
            if (fields.size == 2) fields[0] to Metrics((fields[1].toLongOrNull() ?: 0) * 1000) else null }.toMap().toMutableMap()
        transfers.forEach { val fields = it.trim().split(Regex("\\s+"))
            if (fields.size == 3) values[fields[0]] = (values[fields[0]] ?: Metrics()).copy(rx = fields[1].toLongOrNull() ?: 0, tx = fields[2].toLongOrNull() ?: 0) }
        return values
    }

    private suspend fun createBackend(context: Context): Backend {
        if (!useRootBackend()) return GoBackend(context)
        check(WgQuickBackend.hasKernelSupport()) { "已有 root，但内核不支持 WireGuard，未启动系统 VPN；请启用内核模块或使用桌面组网代理" }
        val shell = com.wireguard.android.util.RootShell(context)
        shell.start()
        rootShell = shell
        return WgQuickBackend(context, shell, com.wireguard.android.util.ToolsInstaller(context, shell)).apply { setMultipleTunnels(true) }
    }

    /**
     * root 独立接口的流量在 Clash 等系统 VPN 抢占默认网络时会被对方的 tun 表接走（已实测）。
     * 只给组网网段加一条优先 lookup：目的 172.1.0.0/16 走 wg-quick 创建的 filesync 表；
     * 不改动 Clash 全局规则，断开时由 [clearRouteRule] 删除。
     *
     * wg-quick 每次重连会重建 netd 网络（表 id 变化），所以不能按表名删除历史规则，
     * 统一按 selector `to 172.1.0.0/16 pref 1000` 循环清理后再添加。
     */
    private fun ensureRouteRule() {
        val shell = rootShell ?: return
        runCatching {
            clearStaleRouteRules(shell)
            val output = mutableListOf<String>()
            val code = shell.run(output, "ip rule add to 172.1.0.0/16 lookup filesync pref 1000")
            routeRuleAdded = code == 0
            if (code != 0) Log.w("MeshController", "组网优先路由规则添加失败: $output")
        }.onFailure { Log.w("MeshController", "组网优先路由规则异常", it) }
    }

    /** 只删除本次会话自己添加的规则；失败不影响隧道清理。 */
    private fun clearRouteRule() {
        if (!routeRuleAdded) return
        runCatching { rootShell?.let { clearStaleRouteRules(it) } }
        routeRuleAdded = false
    }

    private fun clearStaleRouteRules(shell: com.wireguard.android.util.RootShell) {
        for (attempt in 0 until 8) {
            if (shell.run(mutableListOf(), "ip rule del to 172.1.0.0/16 pref 1000") != 0) break
        }
    }

    private fun lanEndpoints(): List<String> = NetworkInterface.getNetworkInterfaces().toList()
        .filter { it.isUp && !it.isLoopback && !it.name.startsWith("tun") }
        .flatMap { it.inetAddresses.toList() }.filter { it.isSiteLocalAddress && !it.isLoopbackAddress }
        .map { if (it is java.net.Inet6Address) "[${it.hostAddress}]:51821" else "${it.hostAddress}:51821" }.distinct().take(8)

    private fun localNetworks(): List<String> = NetworkInterface.getNetworkInterfaces().toList()
        .filter { it.isUp && !it.isLoopback }.flatMap { it.interfaceAddresses }
        .filter { it.address is java.net.Inet4Address && it.address.isSiteLocalAddress }
        .map { "${it.address.hostAddress}/${it.networkPrefixLength}" }

    suspend fun disconnect() {
        val job = session
        session = null
        // 先推进代际：旧 session 的 finally 不再回写状态；cancelAndJoin 结束后也要确认代际未变。
        val generation = epoch.incrementAndGet()
        job?.cancelAndJoin()
        if (epoch.get() == generation) mutableState.value = State()
    }
}
