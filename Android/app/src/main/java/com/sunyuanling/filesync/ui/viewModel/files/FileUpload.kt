// ui/viewModel/files/FileUploadViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.files

import android.app.Application
import android.net.Uri
import android.os.Environment
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.util.DeviceInfoUtil
import com.sunyuanling.filesync.util.RootHelper
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import java.io.File

class FileUploadViewModel(app: Application) : AndroidViewModel(app) {

    private val _uploadState = MutableStateFlow<UploadState>(UploadState.Idle)
    val uploadState: StateFlow<UploadState> = _uploadState

    private val _selectedFiles = MutableStateFlow<List<UploadFileInfo>>(emptyList())
    val selectedFiles: StateFlow<List<UploadFileInfo>> = _selectedFiles

    private val _targetPath = MutableStateFlow("")
    val targetPath: StateFlow<String> = _targetPath

    private val _currentUploadProgress = MutableStateFlow<UploadProgress?>(null)
    val currentUploadProgress: StateFlow<UploadProgress?> = _currentUploadProgress

    private val _hasRootAccess = MutableStateFlow(false)
    val hasRootAccess: StateFlow<Boolean> = _hasRootAccess

    private val _isCheckingRoot = MutableStateFlow(false)
    val isCheckingRoot: StateFlow<Boolean> = _isCheckingRoot

    //可用磁盘列表
    private val _availableDisks = MutableStateFlow<List<String>>(emptyList())
    val availableDisks: StateFlow<List<String>> = _availableDisks

    /** 当前上传协程引用，便于取消。 */
    private var uploadJob: Job? = null

    init {
        checkRootAccess()
    }

    fun checkRootAccess() {
        viewModelScope.launch {
            _isCheckingRoot.value = true
            val hasRoot = RootHelper.checkRootAccess()
            _hasRootAccess.value = hasRoot
            _isCheckingRoot.value = false

            // 设置默认路径
            if (_targetPath.value.isEmpty()) {
                _targetPath.value = if (hasRoot) {
                    "/"
                } else {
                    Environment.getExternalStorageDirectory().absolutePath
                }
            }
        }
    }

    fun requestRootAccess() {
        viewModelScope.launch {
            _isCheckingRoot.value = true
            val granted = RootHelper.requestRootAccess()
            _hasRootAccess.value = granted
            _isCheckingRoot.value = false

            if (granted && _targetPath.value.isEmpty()) {
                _targetPath.value = "/"
            }
        }
    }

    fun setTargetPath(path: String) {
        _targetPath.value = path
    }

    fun addFiles(files: List<UploadFileInfo>) {
        _selectedFiles.value += files
    }

    fun removeFile(file: UploadFileInfo) {
        _selectedFiles.value = _selectedFiles.value.filter { it.uri != file.uri }
    }

    fun clearFiles() {
        _selectedFiles.value = emptyList()
    }

    fun uploadFiles() {
        if (_selectedFiles.value.isEmpty()) {
            _uploadState.value = UploadState.Error("请选择要上传的文件")
            return
        }
        if (_targetPath.value.isEmpty()) {
            _uploadState.value = UploadState.Error("请选择目标路径")
            return
        }
        // 已有上传在跑，忽略再次触发
        if (uploadJob?.isActive == true) return

        uploadJob = viewModelScope.launch {
            _uploadState.value = UploadState.Uploading
            var successCount = 0
            var failCount = 0

            val deviceId = try {
                DeviceInfoUtil.getDeviceId(getApplication())
            } catch (e: Exception) {
                Log.w("FileUpload", "取 deviceId 失败，回退空串", e)
                ""
            }

            _selectedFiles.value.forEachIndexed { index, fileInfo ->
                ensureActive()
                _currentUploadProgress.value = UploadProgress(
                    fileName = fileInfo.name,
                    currentIndex = index + 1,
                    totalCount = _selectedFiles.value.size,
                    progress = 0f,
                    sentBytes = 0,
                    totalBytes = fileInfo.size
                )

                try {
                    val file = File(fileInfo.path)
                    if (!file.exists()) {
                        Log.e("FileUpload", "文件不存在: ${fileInfo.path}")
                        failCount++
                        return@forEachIndexed
                    }

                    val result = ChunkedUploader.upload(
                        file = file,
                        remoteDir = normalizePath(_targetPath.value),
                        options = ChunkedUploader.UploadOptions(deviceId = deviceId)
                    ) { sent, total ->
                        val p = if (total > 0) (sent.toFloat() / total.toFloat()).coerceIn(0f, 1f) else 0f
                        _currentUploadProgress.value = _currentUploadProgress.value?.copy(
                            progress = p,
                            sentBytes = sent,
                            totalBytes = total
                        )
                    }

                    if (result.isSuccess) {
                        successCount++
                        Log.d("FileUpload", "上传成功: ${fileInfo.name} -> ${result.getOrNull()?.storagePath}")
                    } else {
                        failCount++
                        Log.e("FileUpload", "上传失败: ${fileInfo.name}", result.exceptionOrNull())
                    }
                } catch (ce: CancellationException) {
                    // 取消向上传播
                    throw ce
                } catch (e: Exception) {
                    Log.e("FileUpload", "上传异常: ${fileInfo.name}", e)
                    failCount++
                }
            }

            _currentUploadProgress.value = null
            _uploadState.value = if (failCount == 0) {
                UploadState.Success(successCount)
            } else {
                UploadState.PartialSuccess(successCount, failCount)
            }
        }
    }

    /** 取消正在进行的上传。无任务在跑时为空操作。 */
    fun cancelUpload() {
        uploadJob?.cancel()
        uploadJob = null
        _currentUploadProgress.value = null
        _uploadState.value = UploadState.Idle
    }

    fun resetState() {
        _uploadState.value = UploadState.Idle
    }

    /**
     * 标准化路径格式为正斜杠
     */
    private fun normalizePath(path: String): String {
        if (path.isEmpty()) return path
        return path.replace("\\", "/")
    }
}

data class UploadFileInfo(
    val uri: Uri,
    val name: String,
    val path: String,
    val size: Long,
    val mimeType: String?
)

data class UploadProgress(
    val fileName: String,
    val currentIndex: Int,
    val totalCount: Int,
    val progress: Float,
    /** 已发送字节（字节级进度；旧字段 progress 派生自此处）。 */
    val sentBytes: Long = 0,
    /** 总字节（可能等于 UploadFileInfo.size，秒传完成时直接置 totalSize）。 */
    val totalBytes: Long = 0
)

sealed class UploadState {
    data object Idle : UploadState()
    data object Uploading : UploadState()
    data class Success(val count: Int) : UploadState()
    data class PartialSuccess(val successCount: Int, val failCount: Int) : UploadState()
    data class Error(val message: String) : UploadState()
}
