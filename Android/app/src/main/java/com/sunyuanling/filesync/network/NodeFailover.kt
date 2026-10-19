package com.sunyuanling.filesync.network

import android.content.Context
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.share.normalizeShareBase
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

/** 灾备仅改变新请求入口；探测不携带登录令牌，只有同后端入口才允许接管。 */
object NodeFailover {
    data class State(val active: String = "", val message: String = "等待检查", val checking: Boolean = false)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val probeClient = OkHttpClient.Builder().connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(3, TimeUnit.SECONDS).callTimeout(4, TimeUnit.SECONDS)
        .followRedirects(false).build()
    private val state = MutableStateFlow(State())
    val status = state.asStateFlow()
    private var preferences: android.content.SharedPreferences? = null
    @Volatile private var selected: Pair<String, String>? = null
    @Volatile private var checking = false
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
        set(value) { preferences?.edit()?.putBoolean("enabled", value)?.apply(); if (!value) selected = null }
    var backups: String
        get() = preferences?.getString("backups", "") ?: ""
        set(value) {
            val normalized = value.lineSequence().filter { it.isNotBlank() }.map(::normalizeShareBase).distinct().toList()
            require(normalized.size <= 8) { "最多设置 8 个备用入口" }
            preferences?.edit()?.putString("backups", normalized.joinToString("\n"))?.apply()
            selected = null
        }

    fun init(context: Context) {
        if (preferences != null) return
        preferences = context.applicationContext.getSharedPreferences("node_failover", Context.MODE_PRIVATE)
        job = scope.launch { while (isActive) { checkNow(); delay(30_000) } }
    }

    fun effective(configured: String): String = if (enabled) selected?.takeIf { it.first == configured }?.second ?: configured else configured

    suspend fun checkNow() = withContext(Dispatchers.IO) {
        if (checking) return@withContext
        synchronized(this@NodeFailover) { if (checking) return@withContext; checking = true }
        try {
            val configured = AppConfig.getConfiguredBaseUrl()
            val active = effective(configured)
            if (!enabled) { state.value = State(configured, "自动灾备已关闭"); return@withContext }
            state.value = State(active, "检查入口中", true)
            val entries = (listOf(configured) + backups.lineSequence().filter { it.isNotBlank() }.toList()).distinct()
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
                com.sunyuanling.filesync.core.NodeSelector.Input(active, enabled, expected, reports))
            if (configured != AppConfig.getConfiguredBaseUrl()) return@withContext
            if (decision != null) {
                selected = configured to decision
                state.value = State(decision, if (decision == configured) "主入口正常" else "备用入口接管中")
            } else {
                state.value = State(active, "没有可用的同后端入口，等待网络恢复")
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            state.value = State(effective(AppConfig.getConfiguredBaseUrl()), e.message ?: "节点检查失败")
        } finally { checking = false }
    }

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

