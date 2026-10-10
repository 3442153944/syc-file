// core/SyncPlanner.kt
// upload_planner（多路径自适应上传决策核心）的 Kotlin 门面（JNI）。
//
// 对应 Rust 侧 Android/sync_core_jni/src/lib.rs 的 nativePlanner* 一族：核心只做决策
// 不发 HTTP，平台（ChunkedUploader）循环 next → 执行 → report 直到 Done/Failed；
// Task/Report/PersistedState 一律 JSON 字符串跨边界（与 sync_core C ABI 同构），
// 句柄是原生 Box 指针存 Long。
//
// 可用性复用 SyncCore 的加载结果（System.loadLibrary 幂等，同一份 .so）：
// .so 缺失/过旧（无 planner 符号）时 available=false 或调用抛 UnsatisfiedLinkError，
// 均被 runCatching 兜住，ChunkedUploader 自动回退固定分片单路径上传。
package com.sunyuanling.filesync.core

import android.content.Context
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonPrimitive

object SyncPlanner {

    private const val PREFS_NAME = "sync_planner"
    private const val KEY_STATE = "upload_planner_state"

    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    /** 原生库是否可用（与 SyncCore 同一份 .so，加载成功且 ABI ≥ 3）。 */
    val available: Boolean get() = SyncCore.available

    private var appContext: Context? = null

    /** 由 MainActivity 初始化：提供测速样本（PersistedState）的 SharedPreferences 落点。 */
    fun init(context: Context) {
        appContext = context.applicationContext
    }

    /** 上次上传留下的测速样本（PersistedState JSON）；无/未初始化为 null。 */
    fun loadState(): String? = appContext
        ?.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        ?.getString(KEY_STATE, null)

    /** 结束（成功/失败）把本次测速样本写回，下次冷启动 10min 内免重复探测。 */
    fun saveState(stateJson: String) {
        appContext?.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
            ?.edit()?.putString(KEY_STATE, stateJson)?.apply()
    }

    // ---- JNI（实现见 Android/sync_core_jni/src/lib.rs，全部 catch_unwind）----
    private external fun nativePlannerNew(descJson: String): Long
    private external fun nativePlannerNext(handle: Long): String
    private external fun nativePlannerReport(handle: Long, reportJson: String): Int
    private external fun nativePlannerResume(handle: Long, uploadId: String, missingJson: String): Int
    private external fun nativePlannerChunkSize(handle: Long): Long
    private external fun nativePlannerWindows(handle: Long): String
    private external fun nativePlannerExport(handle: Long): String?
    private external fun nativePlannerFree(handle: Long)

    /** 构造 Planner。返回句柄，0=失败（JSON 非法/节点池为空）。 */
    fun newPlanner(descJson: String): Long =
        runCatching { nativePlannerNew(descJson) }.getOrElse { 0L }

    /** 下一个任务 JSON（终态为 {"type":"done"} / failed）；原生异常返回 null。 */
    fun next(handle: Long): String? =
        runCatching { nativePlannerNext(handle) }.getOrNull()

    /** 回报执行结果（Report JSON）。false=句柄无效/JSON 非法/原生异常。 */
    fun report(handle: Long, reportJson: String): Boolean =
        runCatching { nativePlannerReport(handle, reportJson) == 0 }.getOrDefault(false)

    /** 接入会话（首个 upload 与 404 后重新 init 都用它）。 */
    fun resume(handle: Long, uploadId: String, missingJson: String): Boolean =
        runCatching { nativePlannerResume(handle, uploadId, missingJson) == 0 }.getOrDefault(false)

    /** 探测选档后的分片大小；0=未就绪（describe 之前不可调）。 */
    fun chunkSize(handle: Long): Long =
        runCatching { nativePlannerChunkSize(handle) }.getOrElse { 0L }

    /** 每节点当前 AIMD 窗口（id → window），窗口变化日志用；失败返回空表。 */
    fun windows(handle: Long): List<Pair<String, Int>> = runCatching {
        json.parseToJsonElement(nativePlannerWindows(handle)).jsonArray.mapNotNull { e ->
            runCatching {
                val pair = e.jsonArray
                pair[0].jsonPrimitive.content to pair[1].jsonPrimitive.int
            }.getOrNull()
        }
    }.getOrElse { emptyList() }

    /** 导出持久化样本 JSON；失败返回 null。 */
    fun export(handle: Long): String? =
        runCatching { nativePlannerExport(handle) }.getOrNull()

    fun free(handle: Long) {
        runCatching { nativePlannerFree(handle) }
    }
}
