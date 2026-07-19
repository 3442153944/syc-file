// ui/viewModel/preview/PreviewViewModel.kt
// 职责：按预览类型分流加载。
//  - 流式类（图片/视频/音频）：仅构造带 token 的下载 URL，交给 Coil / ExoPlayer 边下边播（支持 Range 动态加载）。
//  - 缓存类（PDF/文本/Office）：先下载到 app cache，再本地渲染；onCleared 删除缓存（"看完即删"）。
package com.sunyuanling.filesync.ui.viewModel.preview

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.file.DownloadParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.previewUtil.OfficeContent
import com.sunyuanling.filesync.previewUtil.OfficeExtractor
import com.sunyuanling.filesync.previewUtil.PreviewCache
import com.sunyuanling.filesync.previewUtil.PreviewType
import com.sunyuanling.filesync.previewUtil.detectPreviewType
import com.sunyuanling.filesync.router.PreviewDestination
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.io.File

/** 预览界面状态。 */
sealed interface PreviewUiState {
    data object Idle : PreviewUiState
    /** 缓存类下载中；[progress] 为 0..1，未知总大小时为 null。 */
    data class Loading(val progress: Float?, val message: String) : PreviewUiState
    /** 图片：交给 Coil 从 [url] 加载。 */
    data class Image(val url: String) : PreviewUiState
    /** 视频/音频：交给 ExoPlayer 从 [url] 流式播放。 */
    data class Media(val url: String, val isVideo: Boolean) : PreviewUiState
    /** PDF：已下载的本地文件，交给 PdfRenderer。 */
    data class Pdf(val file: File) : PreviewUiState
    /** 纯文本：已读取的内容（可能截断）。 */
    data class Text(val text: String, val truncated: Boolean) : PreviewUiState
    /** Office：POI 解析后的结构化内容。 */
    data class Office(val content: OfficeContent) : PreviewUiState
    /** 暂不支持在线预览。 */
    data class Unsupported(val reason: String) : PreviewUiState
    /** 加载/解析失败。 */
    data class Error(val message: String) : PreviewUiState
}

class PreviewViewModel(app: Application) : AndroidViewModel(app) {

    private val _state = MutableStateFlow<PreviewUiState>(PreviewUiState.Idle)
    val state: StateFlow<PreviewUiState> = _state.asStateFlow()

    /** 当前预览类型，界面据此决定标题图标/操作。 */
    var previewType: PreviewType = PreviewType.UNSUPPORTED
        private set

    private var cachedFile: File? = null
    private var started = false
    private var lastPercent = -1

    private companion object {
        const val TEXT_CAP_BYTES = 1_000_000 // 文本预览最多读取 ~1MB
    }

    fun start(args: PreviewDestination) {
        if (started) return
        started = true
        val type = detectPreviewType(args.name, args.extension)
        previewType = type
        viewModelScope.launch {
            when (type) {
                PreviewType.IMAGE ->
                    _state.value = PreviewUiState.Image(buildUrl(args))
                PreviewType.VIDEO ->
                    _state.value = PreviewUiState.Media(buildUrl(args), isVideo = true)
                PreviewType.AUDIO ->
                    _state.value = PreviewUiState.Media(buildUrl(args), isVideo = false)
                PreviewType.PDF ->
                    loadCache(args) { file -> _state.value = PreviewUiState.Pdf(file) }
                PreviewType.TEXT ->
                    loadCache(args) { file -> _state.value = readText(file) }
                PreviewType.OFFICE_WORD, PreviewType.OFFICE_EXCEL, PreviewType.OFFICE_PPT ->
                    loadCache(args) { file ->
                        OfficeExtractor.extract(file, type)
                            .onSuccess { _state.value = PreviewUiState.Office(it) }
                            .onFailure { _state.value = PreviewUiState.Error(it.message ?: "文档解析失败") }
                    }
                PreviewType.UNSUPPORTED ->
                    _state.value = PreviewUiState.Unsupported("暂不支持在线预览该类型文件")
            }
        }
    }

    private suspend fun buildUrl(args: PreviewDestination): String =
        FileApi.buildDownloadUrl(
            DownloadParams(path = args.path, name = args.name, deviceId = args.deviceId)
        )

    private suspend fun loadCache(
        args: PreviewDestination,
        onReady: suspend (File) -> Unit
    ) {
        lastPercent = -1
        _state.value = PreviewUiState.Loading(null, "准备中…")
        val result = PreviewCache.downloadToCache(
            context = getApplication<Application>(),
            path = args.path,
            name = args.name,
            deviceId = args.deviceId
        ) { downloaded, total ->
            // 按 1% 粒度节流，避免高频刷新
            val percent = if (total > 0) ((downloaded * 100) / total).toInt() else -1
            if (percent != lastPercent) {
                lastPercent = percent
                val progress = if (total > 0) downloaded.toFloat() / total else null
                _state.value = PreviewUiState.Loading(progress, "下载中…")
            }
        }
        result.onSuccess { file ->
            cachedFile = file
            _state.value = PreviewUiState.Loading(1f, "解析中…")
            onReady(file)
        }.onFailure {
            _state.value = PreviewUiState.Error(it.message ?: "加载失败")
        }
    }

    private fun readText(file: File): PreviewUiState {
        return try {
            val bytes = ByteArray(TEXT_CAP_BYTES)
            var read = 0
            file.inputStream().use { input ->
                while (read < TEXT_CAP_BYTES) {
                    val n = input.read(bytes, read, TEXT_CAP_BYTES - read)
                    if (n == -1) break
                    read += n
                }
            }
            val truncated = file.length() > TEXT_CAP_BYTES
            PreviewUiState.Text(String(bytes, 0, read, Charsets.UTF_8), truncated)
        } catch (e: Exception) {
            PreviewUiState.Error(e.message ?: "读取文本失败")
        }
    }

    override fun onCleared() {
        super.onCleared()
        // 缓存类文件看完即删
        PreviewCache.delete(cachedFile)
    }
}
