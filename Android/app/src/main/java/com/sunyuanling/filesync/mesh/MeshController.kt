package com.sunyuanling.filesync.mesh

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.Process
import java.io.File
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
        val message: String = "组网未连接", val received: Long = 0, val sent: Long = 0,
        val network: String = "", val backend: String = "", val peers: List<PeerState> = emptyList())
    data class PeerState(val device: String, val address: String, val endpoint: String, val mode: String,
        val handshake: Long, val received: Long, val sent: Long) {
        val label: String get() = when (mode) {
            "lan" -> "局域网直连"
            "direct" -> "公网直连"
            "server" -> "服务器已握手"
            "relay" -> "经中继（对端待验证）"
            "probing" -> "正在探测直连"
            else -> "未验证"
        }
    }
    private val mutableState = MutableStateFlow(State())
    val state = mutableState.asStateFlow()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val mutex = Mutex()
    private val identityMutex = Mutex()
    private data class Identity(val realm: String, val token: String, val device: String,
        val key: String, val pubkey: String, val address: String)
    private var memoryIdentity: Identity? = null

    /** Invoked only after login/verify saved a valid token. No key persistence. */
    suspend fun afterAuthentication() {
        runCatching { authenticatedIdentity() }.onFailure {
            mutableState.value = mutableState.value.copy(message = "组网身份获取失败，请连接时重试")
        }
    }

    private suspend fun authenticatedIdentity(force: Boolean = false): Identity = identityMutex.withLock {
        val realm = AppConfig.getConfiguredBaseUrl()
        val token = Request.getToken()?.takeIf { it.isNotBlank() } ?: throw FatalMeshException("请先登录")
        val device = Request.deviceId()
        memoryIdentity?.takeIf { !force && it.realm == realm && it.token == token && it.device == device }?.let { return@withLock it }
        memoryIdentity = null
        val base = AppConfig.getBaseUrl()
        api(base, "auto-enroll", buildJsonObject {})
        val data = api(base, "identity?device_id=$device")
        val key = data.getValue("private_key").jsonPrimitive.content
        require(android.util.Base64.decode(key, android.util.Base64.DEFAULT).size == 32) { "无效的组网身份" }
        val identity = Identity(realm, token, device, key,
            data.getValue("pubkey").jsonPrimitive.content, data.getValue("address").jsonPrimitive.content)
        if (Request.getToken() != token || AppConfig.getConfiguredBaseUrl() != realm) throw FatalMeshException("登录身份已变化")
        memoryIdentity = identity
        identity
    }
    private var backend: Backend? = null
    private var rootShell: com.wireguard.android.util.RootShell? = null
    private var session: Job? = null
    /** root 专用接口的 172.1.0.0/16 优先规则是否由本次会话添加（断开时清理）。 */
    private var routeRuleAdded = false
    /** 连接代际：disconnect/新连接会推进，旧 session 的收尾不得覆盖新 session 的状态。 */
    private val epoch = AtomicLong(0)
    private var isolatedTest = false
    private var listenPort = 51821
    private var interfaceName = "filesync"
    private val routePriority: Int get() = if (isolatedTest) 999 else 1000
    private val ruleSelector: String get() = "to 172.1.0.0/16" + if (isolatedTest) " uidrange ${Process.myUid()}-${Process.myUid()}" else ""
    fun currentInterfaceName(): String = interfaceName
    private val tunnel = object : Tunnel {
        override fun getName() = interfaceName
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
        isolatedTest = context.packageName.endsWith(".meshtest")
        interfaceName = if (isolatedTest) "filesync-test" else "filesync"
        listenPort = if (isolatedTest) 51823 else 51821
        val generation = epoch.incrementAndGet()
        mutableState.value = State(busy = true, message = "正在登记组网设备", network = networkLabel(context))
        try { MeshForegroundService.start(context) } catch (e: Exception) {
            mutableState.value = mutableState.value.copy(busy = false, message = e.message ?: "无法启动组网状态通知")
            return
        }
        session = scope.launch { mutex.withLock { runSession(context.applicationContext, generation) } }
    }

    private suspend fun CoroutineScope.runSession(context: Context, generation: Long) {
        if (epoch.get() != generation) return
        mutableState.value = State(busy = true, message = "正在登记组网设备", network = networkLabel(context))
        try {
            val realm = AppConfig.getConfiguredBaseUrl()
            val accountToken = Request.getToken()
            var identity = authenticatedIdentity()
            val device = Request.deviceId()
            var key = identity.key
            var address = identity.address
            val engine = backend ?: createBackend(context).also { backend = it }
            var configuration = ""
            var configuredPrefixes = emptySet<String>()
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
                    if (peers.getValue("self").jsonObject.getValue("pubkey").jsonPrimitive.content != identity.pubkey) {
                        identity = authenticatedIdentity(force = true)
                        key = identity.key
                        address = identity.address
                        configuration = ""
                        configuredPeers = emptyMap()
                        delay(1_000)
                        continue
                    }
                    val statistics = metrics(engine)
                    val observed = JsonArray(peers.getValue("peers").jsonArray.map { item ->
                        val peer = item.jsonObject
                        val pubkey = peer.getValue("pubkey").jsonPrimitive.content
                        val lastHandshake = statistics[pubkey]?.handshake ?: 0
                        val attempt = configuredPeers[pubkey]
                        if (attempt != null && lastHandshake > 0 && lastHandshake >= attempt.since && System.currentTimeMillis() - lastHandshake < 180_000)
                            JsonObject(peer + ("confirmed_endpoint" to JsonPrimitive(
                                if (peer["role"]?.jsonPrimitive?.content == "server") attempt.endpoint
                                else statistics[pubkey]?.endpoint?.takeIf { it.isNotBlank() } ?: attempt.endpoint))) else peer
                    })
                    val routes = com.sunyuanling.filesync.core.MeshPlanner.plan(observed, round++, localNetworks())
                    val conf = buildString {
                        // WgQuickBackend writes a temporary file. Give it a
                        // public zero placeholder, then inject the real key via
                        // stdin directly into the kernel. GoBackend stays in RAM.
                        val interfaceKey = if (engine is WgQuickBackend) "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" else key
                        append("[Interface]\nPrivateKey = $interfaceKey\nAddress = $address/32\nListenPort = $listenPort\nMTU = 1280\n")
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
                        val nextPrefixes = routes.flatMap { it.jsonObject.getValue("allowed_ips").jsonArray.map { p -> p.jsonPrimitive.content } }.toSet()
                        if (configuration.isNotEmpty() && engine is WgQuickBackend) {
                            updateRootPeers(context, routes, configuredPrefixes, nextPrefixes)
                        } else {
                            engine.setState(tunnel, Tunnel.State.UP, Config.parse(ByteArrayInputStream(conf.toByteArray())))
                            if (engine is WgQuickBackend) RootMemoryIdentity.install(context, interfaceName, listenPort, key)
                        }
                        configuredPrefixes = nextPrefixes
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
                    publishState(context, address, engine, peers.getValue("peers").jsonArray, routes)
                    api(AppConfig.getBaseUrl(), "heartbeat", buildJsonObject { put("device_id", device); put("endpoint", "");
                        put("lan_endpoints", JsonArray(lanEndpoints().map(::JsonPrimitive))) })
                    failures = 0
                    repeat(3) {
                        delay(5_000)
                        publishState(context, address, engine, peers.getValue("peers").jsonArray, routes)
                    }
                    if (engine.getState(tunnel) != Tunnel.State.UP) throw FatalMeshException("系统已撤销 VPN")
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    if (!isTransient(e)) throw e
                    // 瞬时错误只提示并退避重试，保持已建立的 WG 接口，避免一次网络抖动就拆掉隧道。
                    failures++
                    val waitMillis = retryDelayMillis(failures)
                    mutableState.value = mutableState.value.copy(network = networkLabel(context), message = "网络波动，重试中（${waitMillis / 1000} 秒后重试）")
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
                memoryIdentity = null
                // 旧代际只释放自己的资源；状态只在仍是当前代际时回写，避免覆盖新 session。
                if (epoch.get() == generation) mutableState.value = mutableState.value.copy(running = false, busy = false, peers = emptyList())
            }
        }
    }

    suspend fun useRootBackend(): Boolean = com.sunyuanling.filesync.util.RootHelper.checkRootAccess()

    fun refreshNetwork(context: Context) {
        mutableState.value = mutableState.value.copy(network = networkLabel(context))
    }

    private fun networkLabel(context: Context): String {
        val manager = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val physical = manager.allNetworks.firstOrNull { network ->
            val caps = manager.getNetworkCapabilities(network)
            caps != null && !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
        } ?: return "网络未连接"
        val caps = manager.getNetworkCapabilities(physical)
        val name = when {
            caps?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true -> "Wi-Fi"
            caps?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true -> "移动网络"
            caps?.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) == true -> "以太网"
            else -> "网络"
        }
        val ips = manager.getLinkProperties(physical)?.linkAddresses.orEmpty()
            .filter { it.address is java.net.Inet4Address }.joinToString { it.address.hostAddress.orEmpty() }
        val vpn = manager.allNetworks.any { manager.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true }
        return "$name · $ips" + if (vpn) " · 系统 VPN 已连接" else ""
    }

    private fun publishState(context: Context, address: String, engine: Backend, peers: JsonArray, routes: JsonArray) {
        val stats = metrics(engine)
        val now = System.currentTimeMillis()
        fun fresh(m: Metrics?): Boolean = m != null && m.handshake > 0 && now - m.handshake in 0..179_999
        val server = peers.map { it.jsonObject }.firstOrNull { it["role"]?.jsonPrimitive?.content == "server" }
        val serverKey = server?.get("pubkey")?.jsonPrimitive?.content
        val routeMap = routes.associate { it.jsonObject.getValue("pubkey").jsonPrimitive.content to it.jsonObject }
        val peerStates = peers.map { item ->
            val p = item.jsonObject
            val pubkey = p.getValue("pubkey").jsonPrimitive.content
            val peerAddress = p["address"]?.jsonPrimitive?.content
                ?: p.getValue("allowed_ips").jsonArray.firstOrNull()?.jsonPrimitive?.content?.substringBefore('/') ?: ""
            val route = routeMap[pubkey]
            val metrics = stats[pubkey]
            val endpoint = metrics?.endpoint?.takeUnless { it.isBlank() || it == "(none)" }
                ?: route?.get("endpoint")?.jsonPrimitive?.content.orEmpty()
            val ownsRoute = route?.get("allowed_ips")?.jsonArray?.any { it.jsonPrimitive.content == "$peerAddress/32" } == true
            val viaRelay = routeMap[serverKey]?.get("allowed_ips")?.jsonArray?.any { it.jsonPrimitive.content == "$peerAddress/32" } == true
            val mode = when {
                pubkey == serverKey -> if (fresh(metrics)) "server" else "unknown"
                ownsRoute && fresh(metrics) -> {
                    val host = endpoint.substringBeforeLast(':').removeSurrounding("[", "]")
                    if (p["candidates"]?.jsonArray?.any { it.jsonPrimitive.content == endpoint } == true &&
                        runCatching { java.net.InetAddress.getByName(host).isSiteLocalAddress }.getOrDefault(false)) "lan" else "direct"
                }
                viaRelay && fresh(stats[serverKey]) -> "relay"
                route != null -> "probing"
                else -> "unknown"
            }
            PeerState(p["device_id"]?.jsonPrimitive?.content ?: pubkey.take(8), peerAddress, endpoint, mode,
                metrics?.handshake ?: 0, metrics?.rx ?: 0, metrics?.tx ?: 0)
        }
        val handshakes = stats.values.count(::fresh)
        mutableState.value = State(true, false, address,
            "组网运行，已握手 $handshakes 个对端", stats.values.sumOf { it.rx }, stats.values.sumOf { it.tx },
            networkLabel(context), if (engine is WgQuickBackend) "Root 独立接口" else "系统 VPN", peerStates)
    }

    /** Public peer-only update: preserves kernel interface, UDP mapping and sessions. */
    private fun updateRootPeers(context: Context, routes: JsonArray, previous: Set<String>, next: Set<String>) {
        val shell = checkNotNull(rootShell)
        val config = buildString {
            // addconf preserves interface identity/UDP port; syncconf with an
            // empty [Interface] resets omitted private key and listen port.
            append("[Interface]\n")
            routes.forEach { item ->
                val p = item.jsonObject
                val pubkey = p.getValue("pubkey").jsonPrimitive.content
                val endpoint = p.getValue("endpoint").jsonPrimitive.content
                if (endpoint.isBlank()) return@forEach
                require(pubkey.matches(Regex("[A-Za-z0-9+/]{43}=")) && endpoint.none(Char::isWhitespace)) { "无效的组网对端" }
                append("\n[Peer]\nPublicKey = $pubkey\nPersistentKeepalive = 5\nEndpoint = $endpoint\n")
                val prefixes = p.getValue("allowed_ips").jsonArray.map { it.jsonPrimitive.content }
                if (prefixes.isNotEmpty()) append("AllowedIPs = ${prefixes.joinToString(", ")}\n")
            }
        }
        (previous + next).forEach { prefix ->
            require(prefix.matches(Regex("172\\.1\\.(?:[0-9]{1,3})\\.(?:[0-9]{1,3})/(?:1[6-9]|2[0-9]|3[0-2])"))) { "无效的组网路由" }
        }
        val file = File.createTempFile("mesh-public-peers-", ".conf", context.cacheDir)
        try {
            file.writeText(config)
            val path = file.absolutePath.replace("'", "'\\''")
            val existing = mutableListOf<String>()
            check(shell.run(existing, "wg show $interfaceName peers") == 0) { "读取组网对端失败" }
            val wanted = routes.map { it.jsonObject.getValue("pubkey").jsonPrimitive.content }.toSet()
            for (pubkey in existing.flatMap { it.split(Regex("\\s+")) }.filter { it.isNotBlank() && it !in wanted }) {
                require(pubkey.matches(Regex("[A-Za-z0-9+/]{43}=")))
                check(shell.run(mutableListOf(), "wg set $interfaceName peer $pubkey remove") == 0) { "移除组网对端失败" }
            }
            check(shell.run(mutableListOf(), "wg addconf $interfaceName '$path'") == 0) { "更新组网对端失败" }
            for (prefix in next) check(shell.run(mutableListOf(), "ip route replace $prefix dev $interfaceName table $interfaceName") == 0) { "更新组网路由失败" }
            for (prefix in previous - next) shell.run(mutableListOf(), "ip route del $prefix dev $interfaceName table $interfaceName")
        } finally { file.delete() }
    }

    fun requestDisconnect() { scope.launch { disconnect() } }

    private data class Metrics(val handshake: Long = 0, val rx: Long = 0, val tx: Long = 0, val endpoint: String = "")
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
        check(shell.run(handshakes, "wg show $interfaceName latest-handshakes") == 0) { "读取隧道握手失败" }
        check(shell.run(transfers, "wg show $interfaceName transfer") == 0) { "读取隧道流量失败" }
        val values = handshakes.mapNotNull { val fields = it.trim().split(Regex("\\s+"))
            if (fields.size == 2) fields[0] to Metrics((fields[1].toLongOrNull() ?: 0) * 1000) else null }.toMap().toMutableMap()
        transfers.forEach { val fields = it.trim().split(Regex("\\s+"))
            if (fields.size == 3) values[fields[0]] = (values[fields[0]] ?: Metrics()).copy(rx = fields[1].toLongOrNull() ?: 0, tx = fields[2].toLongOrNull() ?: 0) }
        val endpoints = mutableListOf<String>()
        check(shell.run(endpoints, "wg show $interfaceName endpoints") == 0) { "读取隧道端点失败" }
        endpoints.forEach { val fields = it.trim().split(Regex("\\s+"))
            if (fields.size == 2) values[fields[0]] = (values[fields[0]] ?: Metrics()).copy(endpoint = fields[1]) }
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
            val code = shell.run(output, "ip rule add $ruleSelector lookup $interfaceName pref $routePriority")
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
            if (shell.run(mutableListOf(), "ip rule del $ruleSelector pref $routePriority") != 0) break
        }
    }

    private fun lanEndpoints(): List<String> = NetworkInterface.getNetworkInterfaces().toList()
        .filter { it.isUp && !it.isLoopback && !it.name.startsWith("tun") }
        .flatMap { it.inetAddresses.toList() }.filter { it.isSiteLocalAddress && !it.isLoopbackAddress }
        .map { if (it is java.net.Inet6Address) "[${it.hostAddress}]:$listenPort" else "${it.hostAddress}:$listenPort" }.distinct().take(8)

    private fun localNetworks(): List<String> = NetworkInterface.getNetworkInterfaces().toList()
        .filter { it.isUp && !it.isLoopback }.flatMap { it.interfaceAddresses }
        .filter { it.address is java.net.Inet4Address && it.address.isSiteLocalAddress }
        .map { "${it.address.hostAddress}/${it.networkPrefixLength}" }

    suspend fun disconnect() {
        memoryIdentity = null
        val job = session
        session = null
        // 先推进代际：旧 session 的 finally 不再回写状态；cancelAndJoin 结束后也要确认代际未变。
        val generation = epoch.incrementAndGet()
        job?.cancelAndJoin()
        if (epoch.get() == generation) mutableState.value = State()
    }
}
