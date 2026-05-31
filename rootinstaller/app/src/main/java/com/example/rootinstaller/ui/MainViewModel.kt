package com.example.rootinstaller.ui

import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.rootinstaller.core.FileManager
import com.example.rootinstaller.core.InstallManager
import com.example.rootinstaller.core.RootShell
import com.example.rootinstaller.model.ApkInfo
import com.example.rootinstaller.model.FileItem
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

data class UiState(
    val rootAvailable: Boolean? = null,
    val currentPath: String = "/storage/emulated/0",
    val files: List<FileItem> = emptyList(),
    val isLoading: Boolean = false,
    val error: String? = null,
    val selectedApk: ApkInfo? = null,
    val installResult: InstallResult? = null,
    val pathHistory: List<String> = listOf("/storage/emulated/0"),
    val allowDowngrade: Boolean = true,
    val allowReplace: Boolean = true,
    val isDefaultInstaller: Boolean = false,
)

data class InstallResult(val success: Boolean, val message: String)

class MainViewModel : ViewModel() {

    private val _state = MutableStateFlow(UiState())
    val state = _state.asStateFlow()

    init {
        viewModelScope.launch {
            val ok = RootShell.isAvailable()
            Log.d("RootInstaller", "root available: $ok")
            _state.update { it.copy(rootAvailable = ok) }
            if (ok) {
                navigateTo("/storage/emulated/0")
                checkIsDefaultInstaller()
            }
        }
    }

    // ─── 文件浏览 ────────────────────────────────────────────

    fun navigateTo(path: String) {
        viewModelScope.launch {
            _state.update { it.copy(isLoading = true, error = null) }
            FileManager.listDir(path).fold(
                onSuccess = { files ->
                    Log.d("RootInstaller", "got ${files.size} items in $path")
                    _state.update { s ->
                        val newHistory = if (path != s.currentPath) s.pathHistory + path else s.pathHistory
                        s.copy(currentPath = path, files = files, isLoading = false, pathHistory = newHistory)
                    }
                },
                onFailure = { e ->
                    _state.update { it.copy(isLoading = false, error = "无法读取目录: ${e.message}") }
                }
            )
        }
    }

    fun navigateUp() {
        val history = _state.value.pathHistory
        if (history.size <= 1) return
        val newHistory = history.dropLast(1)
        navigateBack(newHistory.last(), newHistory)
    }

    private fun navigateBack(path: String, history: List<String>) {
        viewModelScope.launch {
            _state.update { it.copy(isLoading = true, error = null) }
            FileManager.listDir(path).fold(
                onSuccess = { files ->
                    _state.update { it.copy(currentPath = path, files = files, isLoading = false, pathHistory = history) }
                },
                onFailure = { e ->
                    _state.update { it.copy(isLoading = false, error = "无法读取目录: ${e.message}") }
                }
            )
        }
    }

    fun refresh() = navigateTo(_state.value.currentPath)

    // ─── APK 选择与安装 ──────────────────────────────────────

    fun selectApk(context: Context, item: FileItem) {
        if (!item.isApk) return
        viewModelScope.launch {
            val apkInfo = parseApkInfo(context, item.path) ?: return@launch
            _state.update { it.copy(selectedApk = apkInfo) }
        }
    }

    /** 从外部 Intent 传入 APK（文件管理器/微信等点击 APK 跳转） */
    fun handleIncomingApk(context: Context, uri: Uri) {
        viewModelScope.launch {
            // content:// 需要先复制出来才能解析
            val apkPath = resolveUriToPath(context, uri) ?: return@launch
            val apkInfo = parseApkInfo(context, apkPath) ?: return@launch
            _state.update { it.copy(selectedApk = apkInfo) }
        }
    }

    private suspend fun resolveUriToPath(context: Context, uri: Uri): String? =
        withContext(Dispatchers.IO) {
            try {
                when (uri.scheme) {
                    "file" -> uri.path
                    "content" -> {
                        // 复制到临时目录
                        val tmp = File(context.cacheDir, "incoming_${System.currentTimeMillis()}.apk")
                        context.contentResolver.openInputStream(uri)?.use { input ->
                            tmp.outputStream().use { input.copyTo(it) }
                        }
                        tmp.absolutePath
                    }
                    else -> null
                }
            } catch (e: Exception) {
                Log.e("RootInstaller", "resolveUri failed: ${e.message}")
                null
            }
        }

    fun dismissApkSheet() = _state.update { it.copy(selectedApk = null) }

    fun install() {
        val apk = _state.value.selectedApk ?: return
        viewModelScope.launch {
            _state.update { it.copy(isLoading = true) }
            val result = InstallManager.install(
                apkPath = apk.path,
                options = InstallManager.InstallOptions(
                    allowReplace = _state.value.allowReplace,
                    allowDowngrade = _state.value.allowDowngrade,
                )
            )
            _state.update {
                it.copy(
                    isLoading = false,
                    selectedApk = null,
                    installResult = InstallResult(result.success, result.message)
                )
            }
        }
    }

    fun clearInstallResult() = _state.update { it.copy(installResult = null) }
    fun toggleDowngrade(v: Boolean) = _state.update { it.copy(allowDowngrade = v) }
    fun toggleReplace(v: Boolean) = _state.update { it.copy(allowReplace = v) }

    // ─── 设置默认安装器 ──────────────────────────────────────

    /** 检查是否已经是默认安装器 */
    private suspend fun checkIsDefaultInstaller() {
        val result = RootShell.execRoot(
            "cmd package get-default-browser 2>/dev/null; " +
                    "pm resolve-activity --brief -a android.intent.action.INSTALL_PACKAGE 2>/dev/null | tail -1"
        )
        val isDefault = result.stdout.contains("com.example.rootinstaller")
        _state.update { it.copy(isDefaultInstaller = isDefault) }
    }

    /**
     * 用 root 设置本 App 为默认 APK 安装器。
     * 原理：写入 preferred-activity 记录，相当于用户在"始终"弹窗里选了本 App。
     */
    fun setAsDefaultInstaller() {
        viewModelScope.launch {
            val pkg = "com.example.rootinstaller"
            val activity = "$pkg.MainActivity"

            // 清除系统默认安装器的 preferred 记录
            val clear = RootShell.execRoot("pm clear-default-preferred-apps com.android.packageinstaller 2>/dev/null; " +
                    "pm clear-default-preferred-apps com.google.android.packageinstaller 2>/dev/null; " +
                    "pm clear-default-preferred-apps com.oppo.packageinstaller 2>/dev/null")
            Log.d("RootInstaller", "clear defaults: ${clear.output}")

            // 设置本 App 为 INSTALL_PACKAGE 的默认处理器
            val set = RootShell.execRoot(
                "pm set-default-browser $pkg 2>/dev/null || true"
            )

            // ColorOS/Android 16 上用 cmd activity 更可靠
            val set2 = RootShell.execRoot(
                """cmd activity set-preferred-activity \
                    --user 0 \
                    --action android.intent.action.INSTALL_PACKAGE \
                    --data-scheme content \
                    --mime-type application/vnd.android.package-archive \
                    --component $activity 2>/dev/null || true"""
            )
            Log.d("RootInstaller", "set default: ${set2.output}")

            checkIsDefaultInstaller()

            _state.update {
                it.copy(installResult = InstallResult(true, "已尝试设置为默认安装器，建议手动确认"))
            }
        }
    }

    // ─── 解析 APK 信息 ───────────────────────────────────────

    private fun parseApkInfo(context: Context, apkPath: String): ApkInfo? {
        return try {
            val pm = context.packageManager
            @Suppress("DEPRECATION")
            val pkgInfo = pm.getPackageArchiveInfo(apkPath, PackageManager.GET_ACTIVITIES)
                ?: return null
            pkgInfo.applicationInfo?.sourceDir = apkPath
            pkgInfo.applicationInfo?.publicSourceDir = apkPath

            val appName = pkgInfo.applicationInfo?.loadLabel(pm)?.toString() ?: pkgInfo.packageName
            val icon = pkgInfo.applicationInfo?.loadIcon(pm)
            val installedInfo = try { pm.getPackageInfo(pkgInfo.packageName, 0) }
            catch (e: PackageManager.NameNotFoundException) { null }

            @Suppress("DEPRECATION")
            ApkInfo(
                path = apkPath,
                packageName = pkgInfo.packageName,
                appName = appName,
                versionName = pkgInfo.versionName ?: "未知",
                versionCode = pkgInfo.longVersionCode,
                icon = icon,
                installedVersionName = installedInfo?.versionName,
                installedVersionCode = installedInfo?.longVersionCode
            )
        } catch (e: Exception) {
            Log.e("RootInstaller", "parseApkInfo failed: ${e.message}")
            null
        }
    }
}