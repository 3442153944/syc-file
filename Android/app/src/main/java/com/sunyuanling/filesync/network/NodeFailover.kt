package com.sunyuanling.filesync.network

import android.content.Context
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.share.normalizeShareBase
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

/** 灾备仅改变新请求入口；探测不携带登录令牌，只有同后端入口才允许接管。 */
object NodeFailover {
    data class State(val active: String = "", val message: String = "等待检查", val checking: Boolean = false,
        val nodes: List<com.sunyuanling.filesync.core.NodeSelector.Health> = emptyList())
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val probeClient = OkHttpClient.Builder().connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(3, TimeUnit.SECONDS).callTimeout(4, TimeUnit.SECONDS)
        .followRedirects(false).build()
    private val state = MutableStateFlow(State())
    val status = state.asStateFlow()
    private var preferences: android.content.SharedPreferences? = null
    @Volatile private var selected: Pair<String, String>? = null
    private val selectionMutex = Mutex()
    private var appContext: Context? = null
    private var job: Job? = null
    private var lastFailureCheck = 0L
    fun reportFailure(url: String) {
        if (!url.startsWith(effective(AppConfig.getConfiguredBaseUrl()) + "/")) return
        synchronized(this) {
            val now = android.os.SystemClock.elapsedRealtime()
            if (now - lastFailureCheck < 5_000) return
            lastFailureCheck = now
        }
        scope.launch { checkNow() }
    }
    var enabled: Boolean
        get() = preferences?.getBoolean("enabled", true) ?: true
        set(value) { preferences?.edit()?.putBoolean("enabled", value)?.apply(); scope.launch { checkNow() } }
    var backups: String
        get() = preferences?.getString("backups", "") ?: ""
        set(value) {
            val normalized = value.lineSequence().filter { it.isNotBlank() }.map(::normalizeShareBase).distinct().toList()
            require(normalized.size <= 8) { "最多设置 8 个备用入口" }
            preferences?.edit()?.putString("backups", normalized.joinToString("\n"))?.apply()
        }

    fun init(context: Context) {
        if (preferences != null) return
        preferences = context.applicationContext.getSharedPreferences("node_failover", Context.MODE_PRIVATE)
        appContext = context.applicationContext
        val configured = AppConfig.getConfiguredBaseUrl()
        preferences?.getString("selected:$configured", null)?.let { selected = configured to it }
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as android.net.ConnectivityManager
        connectivity.registerDefaultNetworkCallback(object : android.net.ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: android.net.Network) { scope.launch { checkNow() } }
            override fun onLost(network: android.net.Network) { scope.launch { checkNow() } }
        })
        job = scope.launch { while (isActive) { checkNow(); delay(30_000) } }
    }

    fun effective(configured: String): String = selected?.takeIf { it.first == configured }?.second ?: configured

    /** 手动选择也必须通过实时探测与同后端校验；关闭自动灾备不影响手动选择。 */
    suspend fun selectNow(url: String) = refresh(normalizeShareBase(url))

    private fun activate(configured: String, url: String) {
        val previous = effective(configured)
        selected = configured to url
        preferences?.edit()?.putString("selected:$configured", url)?.apply()
        if (previous != url) appContext?.let {
            com.example.filesync.data.sync.WebSocketManager.onEndpointChanged(it)
        }
    }

    suspend fun checkNow() = refresh(null)

    private suspend fun refresh(requested: String?) = withContext(Dispatchers.IO) { selectionMutex.withLock {
        try {
            val configured = AppConfig.getConfiguredBaseUrl()
            val active = effective(configured)
            state.value = state.value.copy(active = active, message = "检查入口中", checking = true)
            val entries = (listOf(configured) + backups.lineSequence().filter { it.isNotBlank() }.toList()).distinct()
            require(requested == null || requested in entries) { "请先保存该备用入口" }
            val reports = coroutineScope { entries.map { url -> async {
                val started = android.os.SystemClock.elapsedRealtime()
                val backend = probe(url)
                com.sunyuanling.filesync.core.NodeSelector.Health(url, backend != null,
                    android.os.SystemClock.elapsedRealtime() - started, backend.orEmpty())
            } }.awaitAll() }
            val key = "backend:$configured"
            reports.firstOrNull { it.id == configured && it.reachable }?.let {
                preferences?.edit()?.putString(key, it.backend)?.apply()
            }
            val expected = preferences?.getString(key, null).orEmpty()
            // Kotlin 只提交探测结果；健康入口保留、同后端校验和延迟排序全部交给 Rust。
            val decision = com.sunyuanling.filesync.core.NodeSelector.select(
                com.sunyuanling.filesync.core.NodeSelector.Input(requested ?: active, requested == null && enabled, expected, reports))
            if (configured != AppConfig.getConfiguredBaseUrl()) return@withContext
            if (requested != null && decision != requested) {
                state.value = State(active, "所选入口不可达或不属于同一后端", false, reports)
                return@withContext
            }
            if (decision != null) {
                activate(configured, decision)
                state.value = State(decision, if (requested != null) "已手动切换入口" else if (!enabled) "自动灾备已关闭" else if (decision == configured) "主入口正常" else "备用入口接管中", false, reports)
            } else {
                state.value = State(active, if (!enabled) "自动灾备已关闭，当前入口不可达" else "没有可用的同后端入口，等待网络恢复", false, reports)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            state.value = state.value.copy(active = effective(AppConfig.getConfiguredBaseUrl()), message = e.message ?: "节点检查失败", checking = false)
        } finally { state.value = state.value.copy(checking = false) }
    } }

    private fun probe(base: String): String? = runCatching {
        probeClient.newCall(Request.Builder().url("$base/v1/ping").build()).execute().use {
            if (!it.isSuccessful) return@use null
            val obj = com.sunyuanling.filesync.network.Request.json.parseToJsonElement(it.body?.string().orEmpty()).jsonObject
            if (obj["code"]?.jsonPrimitive?.intOrNull != 200) return@use null
            val data = obj["data"]?.jsonObject ?: return@use null
            if (data["status"]?.jsonPrimitive?.content != "ok") return@use null
            data["node"]?.jsonPrimitive?.content?.takeIf(String::isNotBlank)
        }
    }.getOrNull()
}

