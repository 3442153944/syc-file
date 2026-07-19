// update/UpdateController.kt
// 应用更新客户端（进程级单例）：
//  - 检查更新：拉 /v1/update/check，比对本机 version_code
//  - WS 推送：监听 WebSocketManager 的 app_update 事件，主动触发检查
//  - 下载：复用 /v1/file/download（buildDownloadUrl + OkHttp 流式，支持 Range），落 app 外部缓存
//  - 校验：下载后按 blake3 比对 file_hash，防止装到损坏/被篡改的 APK
//  - 安装：FileProvider + ACTION_VIEW 触发系统安装框（非静默，符合 §9.4）
package com.sunyuanling.filesync.update

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.util.Log
import androidx.core.content.FileProvider
import com.sunyuanling.filesync.api.file.DownloadParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.api.update.AppReleaseDto
import com.sunyuanling.filesync.api.update.UpdateApi
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsMessage
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.util.Blake3Util
import com.sunyuanling.filesync.util.RootHelper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.contentOrNull
import okhttp3.Request as OkRequest
import java.io.File
import kotlin.coroutines.coroutineContext

object UpdateController {

    private const val TAG = "UpdateController"

    /** 可用更新：为 null 表示当前无待处理更新。 */
    data class Available(val release: AppReleaseDto, val mandatory: Boolean)
    private val _available = MutableStateFlow<Available?>(null)
    val available: StateFlow<Available?> = _available.asStateFlow()

    /** 下载/安装阶段状态。 */
    sealed interface DownloadState {
        data object Idle : DownloadState
        data class Downloading(val progress: Float?) : DownloadState
        data object Verifying : DownloadState
        data object ReadyToInstall : DownloadState
        data class Error(val message: String) : DownloadState
    }
    private val _downloadState = MutableStateFlow<DownloadState>(DownloadState.Idle)
    val downloadState: StateFlow<DownloadState> = _downloadState.asStateFlow()

    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    @Volatile private var observing = false
    // 用户主动忽略的版本号：同一版本不再自动弹（强制更新不受此限）
    @Volatile private var dismissedVersion: Long = -1

    /** 本机 versionCode。 */
    fun currentVersionCode(context: Context): Long {
        return try {
            val info = context.packageManager.getPackageInfo(context.packageName, 0)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.longVersionCode
            else @Suppress("DEPRECATION") info.versionCode.toLong()
        } catch (e: Exception) {
            Log.w(TAG, "读取 versionCode 失败: ${e.message}")
            0L
        }
    }

    /** 启动 WS app_update 监听（幂等）。发布事件到达即触发一次检查。 */
    fun startObserving(context: Context) {
        if (observing) return
        observing = true
        val appContext = context.applicationContext
        scope.launch {
            WebSocketManager.events.collect { msg ->
                if (msg is WsMessage.Text && isAppUpdateEvent(msg.content)) {
                    Log.i(TAG, "收到 WS app_update 推送，触发检查")
                    checkForUpdate(appContext)
                }
            }
        }
    }

    private fun isAppUpdateEvent(text: String): Boolean {
        return try {
            val obj = json.parseToJsonElement(text).jsonObject
            val type = obj["type"]?.jsonPrimitive?.contentOrNull
            // 后端广播的 type == "app_update"；content 里也带 event=app_update 兜底
            type == "app_update"
        } catch (e: Exception) {
            false
        }
    }

    /** 检查更新（后台）。有新版本且未被忽略则填充 available。 */
    fun checkForUpdate(context: Context, manual: Boolean = false) {
        val appContext = context.applicationContext
        scope.launch {
            val code = currentVersionCode(appContext)
            val result = UpdateApi.check(code)
            result.onSuccess { resp ->
                val data = resp.data
                if (data != null && data.hasUpdate && data.release != null) {
                    val rel = data.release
                    if (manual || rel.versionCode != dismissedVersion || data.mandatory) {
                        _available.value = Available(rel, data.mandatory)
                    }
                } else if (manual) {
                    // 手动检查且无更新：清空，交由 UI 提示"已是最新"
                    _available.value = null
                }
            }.onFailure {
                Log.w(TAG, "检查更新失败: ${it.message}")
            }
        }
    }

    /** 忽略当前可用更新（非强制才允许）。 */
    fun dismiss() {
        _available.value?.let { if (!it.mandatory) dismissedVersion = it.release.versionCode }
        _available.value = null
        _downloadState.value = DownloadState.Idle
    }

    /** 下载 APK → 校验 blake3 → 触发系统安装。 */
    fun downloadAndInstall(context: Context) {
        val appContext = context.applicationContext
        val rel = _available.value?.release ?: return
        scope.launch {
            _downloadState.value = DownloadState.Downloading(null)
            val apk = downloadApk(appContext, rel)
            if (apk == null) return@launch // 状态已置 Error

            // 校验
            if (rel.fileHash.isNotBlank()) {
                _downloadState.value = DownloadState.Verifying
                val actual = runCatching { computeBlake3Hex(apk) }.getOrNull()
                if (actual == null || !actual.equals(rel.fileHash, ignoreCase = true)) {
                    apk.delete()
                    _downloadState.value = DownloadState.Error("安装包校验失败，请重试")
                    return@launch
                }
            }

            _downloadState.value = DownloadState.ReadyToInstall
            // 优先 root 安装（pm install -r -d）：绕过"降级/同版本禁止安装"，并可处理签名冲突；
            // 无 root 时回退系统安装器（FileProvider + ACTION_VIEW）。
            if (RootHelper.checkRootAccess()) {
                rootInstall(appContext, apk)
            } else {
                withContext(Dispatchers.Main) { launchInstall(appContext, apk) }
            }
        }
    }

    /**
     * Root 安装：先把 APK 暂存到 /data/local/tmp（pm 可读，规避 SELinux 读 app 目录），
     * 再 `pm install -r -d` 强制安装（-r 保留数据重装、-d 允许降级/同版本）。
     * 若因证书不一致（签名冲突）失败，回退"卸载后重装"（会清除本应用私有数据，如登录态；
     * 外部存储 /FileSync 下的配置不受影响）。
     */
    private suspend fun rootInstall(context: Context, apk: File) {
        val pkg = context.packageName
        val apkPath = apk.absolutePath
        val staged = "/data/local/tmp/filesync_update.apk"

        // 暂存到 tmp（pm 可读，规避 SELinux）；失败则直接用原路径安装
        val stageOk = RootHelper.executeRootCommand(
            "cp \"$apkPath\" \"$staged\" && chmod 644 \"$staged\""
        ).isSuccess
        val installPath = if (stageOk) staged else apkPath

        // 第一次尝试：-r 保留数据、-d 允许降级/同版本。签名匹配时成功；
        // 成功=重装自身，系统会终止本进程（属正常，用户重开即新版本）。失败不会杀本进程，可读到原因。
        // `|| true` 保证退出码 0，从而拿到完整 stdout。
        val out = RootHelper.executeRootCommand(
            "pm install -r -d \"$installPath\" 2>&1 || true"
        ).getOrDefault("")

        if (out.contains("Success", ignoreCase = true)) {
            apk.delete()
            _downloadState.value = DownloadState.ReadyToInstall
            Log.i(TAG, "root 安装成功（本进程将被系统终止）")
            return
        }

        if (isSignatureConflict(out)) {
            // 签名冲突（如 debug 签名的已装应用去装 release 包）：pm 无法直接覆盖，只能卸载后重装。
            // ⚠ 关键：卸载会杀掉本应用进程，所以卸载+重装必须在【同一个】root 进程里顺序完成，
            //   且输出全部重定向到文件/黑洞——否则本进程一死，管道关闭，pm 写 stdout 触发 EPIPE 会被杀，
            //   装到一半 → 设备上没有应用。这样即使本进程被杀，root 子进程（uid 0，卸载不波及）也会把重装跑完。
            Log.w(TAG, "签名冲突，交由单个 root 进程原子卸载重装")
            _downloadState.value = DownloadState.Error(
                "检测到签名冲突（安装包签名与已装版本不一致）。正在卸载后重装——" +
                        "应用会短暂退出，请稍候重新打开。注意：这会清除本应用的登录态等私有数据。"
            )
            val atomic = "pm uninstall \"$pkg\" >/dev/null 2>&1; " +
                    "pm install -d \"$installPath\" >/data/local/tmp/fs_install.log 2>&1; " +
                    "rm -f \"$staged\""
            RootHelper.executeRootCommand(atomic)
            return
        }

        if (stageOk) RootHelper.executeRootCommand("rm -f \"$staged\"") // best effort
        _downloadState.value = DownloadState.Error(
            "root 安装失败：${out.trim().takeIf { it.isNotEmpty() } ?: "未知错误"}"
        )
    }

    /** pm 安装输出是否为证书/签名不一致类失败。 */
    private fun isSignatureConflict(output: String): Boolean {
        val o = output.uppercase()
        return o.contains("INCONSISTENT_CERTIFICATES") ||
                o.contains("UPDATE_INCOMPATIBLE") ||
                o.contains("SIGNATURE")
    }

    /** 缓存目录：优先外部缓存（放得下大 APK），否则内部缓存。 */
    private fun updateDir(context: Context): File =
        File(context.externalCacheDir ?: context.cacheDir, "update").apply { if (!exists()) mkdirs() }

    private suspend fun downloadApk(context: Context, rel: AppReleaseDto): File? {
        val target = File(updateDir(context), "app_${rel.versionCode}.apk")
        // 已有完整缓存（大小匹配）直接复用
        if (target.exists() && rel.fileSize > 0 && target.length() == rel.fileSize) {
            return target
        }
        val tmp = File(target.absolutePath + ".part")
        return try {
            val url = FileApi.buildDownloadUrl(
                DownloadParams(path = rel.filePath, name = rel.fileName)
            )
            val token = Request.getToken()
            val builder = OkRequest.Builder().url(url).get()
            token?.let { builder.header("Token", it) }

            Request.client.newCall(builder.build()).execute().use { resp ->
                if (!resp.isSuccessful) {
                    _downloadState.value = DownloadState.Error("下载失败：HTTP ${resp.code}")
                    return null
                }
                val body = resp.body ?: run {
                    _downloadState.value = DownloadState.Error("下载失败：响应为空")
                    return null
                }
                val total = if (rel.fileSize > 0) rel.fileSize else body.contentLength()
                body.byteStream().use { input ->
                    tmp.outputStream().use { output ->
                        val buf = ByteArray(64 * 1024)
                        var downloaded = 0L
                        var lastPercent = -1
                        while (true) {
                            coroutineContext.ensureActive()
                            val n = input.read(buf)
                            if (n == -1) break
                            output.write(buf, 0, n)
                            downloaded += n
                            if (total > 0) {
                                val p = ((downloaded * 100) / total).toInt()
                                if (p != lastPercent) {
                                    lastPercent = p
                                    _downloadState.value = DownloadState.Downloading(downloaded.toFloat() / total)
                                }
                            }
                        }
                        output.flush()
                    }
                }
            }
            if (!tmp.renameTo(target)) {
                tmp.copyTo(target, overwrite = true); tmp.delete()
            }
            target
        } catch (e: Exception) {
            tmp.delete()
            _downloadState.value = DownloadState.Error(e.message ?: "下载失败")
            null
        }
    }

    private fun computeBlake3Hex(file: File): String {
        val hasher = Blake3Util.newHasher()
        file.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buf)
                if (n == -1) break
                if (n == buf.size) hasher.update(buf) else hasher.update(buf.copyOf(n))
            }
        }
        return Blake3Util.toHex(hasher.digest())
    }

    private fun launchInstall(context: Context, apk: File) {
        try {
            // Android 8.0+ 需"安装未知应用"授权；未授权则先引导到设置页
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O &&
                !context.packageManager.canRequestPackageInstalls()
            ) {
                val settings = Intent(
                    Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:${context.packageName}")
                ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                context.startActivity(settings)
                // 用户授权后可再次点击安装；此处不强行拉起安装
            }
            val apkUri: Uri = FileProvider.getUriForFile(
                context, "${context.packageName}.fileprovider", apk
            )
            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(apkUri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            context.startActivity(intent)
        } catch (e: Exception) {
            Log.e(TAG, "拉起安装失败: ${e.message}", e)
            _downloadState.value = DownloadState.Error("拉起安装失败：${e.message}")
        }
    }
}
