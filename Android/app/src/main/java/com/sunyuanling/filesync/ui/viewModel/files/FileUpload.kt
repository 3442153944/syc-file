// ui/viewModel/files/FileUploadViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.files

import android.app.Application
import android.net.Uri
import android.provider.OpenableColumns
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.api.file.UploadSpace
import com.sunyuanling.filesync.util.UploadStaging
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.NonCancellable
import kotlin.coroutines.coroutineContext
import com.sunyuanling.filesync.util.DeviceInfoUtil
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

    //可用磁盘列表
    private val _availableDisks = MutableStateFlow<List<String>>(emptyList())
    val availableDisks: StateFlow<List<String>> = _availableDisks

    /** 当前上传协程引用，便于取消。 */
    private var uploadJob: Job? = null
    private val stagingRoot get() = File(getApplication<Application>().cacheDir, "upload-selection")
    private var importJob: Job? = null
    private val _importing = MutableStateFlow(false)
    val importing: StateFlow<Boolean> = _importing
    private val _space = MutableStateFlow<UploadSpace?>(null)
    val space: StateFlow<UploadSpace?> = _space
    private val _spaceError = MutableStateFlow<String?>(null)
    val spaceError: StateFlow<String?> = _spaceError

    init {
        refreshUploadSpace()
    }

    fun refreshUploadSpace() {
        viewModelScope.launch {
            _spaceError.value = null
            FileApi.uploadSpace().onSuccess { response ->
                _space.value = response.data
                val current = response.data
                if (current != null) {
                    if (_targetPath.value.isEmpty()) _targetPath.value = current.defaultPath.ifEmpty { current.path }
                    if (!current.enabled || current.frozen) _spaceError.value = "上传空间已停用或到期"
                } else {
                    // 超级管理员没有独立账号空间时，仍可使用服务端允许的目录。
                    FileApi.getAvailableDisks().onSuccess { disks ->
                        if (_targetPath.value.isEmpty()) _targetPath.value = disks.data?.allowedDisks
                            ?.firstOrNull { it.isAccessible }?.path.orEmpty()
                        if (_targetPath.value.isEmpty()) _spaceError.value = "尚未分配可用上传空间"
                    }.onFailure { _spaceError.value = it.message ?: "读取上传目录失败" }
                }
            }.onFailure { _spaceError.value = it.message ?: "读取上传空间失败" }
        }
    }

    fun importFiles(uris: List<Uri>) {
        if (uris.isEmpty() || importJob?.isActive == true || uploadJob?.isActive == true) return
        importJob = viewModelScope.launch {
            _importing.value = true
            var pending: File? = null
            try {
                for (uri in uris) {
                    val imported = withContext(Dispatchers.IO) {
                        val resolver = getApplication<Application>().contentResolver
                        val name = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
                            if (cursor.moveToFirst()) cursor.getString(0) else null
                        } ?: "file"
                        val jobContext = coroutineContext
                        val copy = resolver.openInputStream(uri)?.use { input ->
                            UploadStaging.copy(stagingRoot, name, input) { jobContext.ensureActive() }
                        } ?: error("无法读取所选文件")
                        pending = copy
                        UploadFileInfo(uri, name, copy.absolutePath, copy.length(), resolver.getType(uri))
                    }
                    _selectedFiles.value += imported
                    pending = null
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                _uploadState.value = UploadState.Error(error.message ?: "导入文件失败")
            } finally {
                withContext(NonCancellable + Dispatchers.IO) {
                    pending?.let { UploadStaging.remove(stagingRoot, it.absolutePath) }
                }
                _importing.value = false
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
        if (uploadJob?.isActive == true || _importing.value) return
        _selectedFiles.value = _selectedFiles.value.filter { it.path != file.path }
        viewModelScope.launch(Dispatchers.IO) { UploadStaging.remove(stagingRoot, file.path) }
    }

    fun clearFiles() {
        if (uploadJob?.isActive == true || _importing.value) return
        val old = _selectedFiles.value
        _selectedFiles.value = emptyList()
        viewModelScope.launch(Dispatchers.IO) { old.forEach { UploadStaging.remove(stagingRoot, it.path) } }
    }

    fun uploadFiles() {
        if (_importing.value) return
        _spaceError.value?.let { _uploadState.value = UploadState.Error(it); return }
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
        val files = _selectedFiles.value.toList()
        val destination = normalizePath(_targetPath.value)

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

            files.forEachIndexed { index, fileInfo ->
                ensureActive()
                _currentUploadProgress.value = UploadProgress(
                    fileName = fileInfo.name,
                    currentIndex = index + 1,
                    totalCount = files.size,
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
                        remoteDir = destination,
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
            refreshUploadSpace()
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

    override fun onCleared() {
        importJob?.cancel()
        uploadJob?.cancel()
        val paths = _selectedFiles.value.map { it.path }
        kotlinx.coroutines.CoroutineScope(Dispatchers.IO).launch {
            importJob?.join()
            uploadJob?.join()
            paths.forEach { UploadStaging.remove(stagingRoot, it) }
        }
        super.onCleared()
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
