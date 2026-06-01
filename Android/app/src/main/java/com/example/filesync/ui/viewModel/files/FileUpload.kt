// ui/viewModel/files/FileUploadViewModel.kt
package com.example.filesync.ui.viewModel.files

import android.content.Context
import android.net.Uri
import android.os.Environment
import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.example.filesync.network.Request
import com.example.filesync.util.RootHelper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.RequestBody.Companion.asRequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okio.Buffer
import okio.BufferedSink
import okio.ForwardingSink
import okio.Sink
import okio.buffer
import java.io.File

class FileUploadViewModel : ViewModel() {

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

    suspend fun checkFileExists(path: String, fileName: String): CheckFileResult? {
        var result: CheckFileResult? = null

        withContext(Dispatchers.IO) {
            try {
                val normalizedPath = normalizePath(path)
                val url = "${Request.baseUrl}/file/upload"
                val token = Request.getToken()

                // 构建 JSON body
                val requestBody = """
                {
                    "path": "$normalizedPath",
                    "name": "$fileName",
                    "action": "check"
                }
            """.trimIndent()

                Log.d("FileUpload", "检查文件: path=$normalizedPath, name=$fileName")

                val request = okhttp3.Request.Builder()
                    .url(url)
                    .apply {
                        token?.let { header("Token", it) }
                    }
                    .post(
                        requestBody.toRequestBody(
                            "application/json".toMediaTypeOrNull()
                        )
                    )
                    .build()

                val response = Request.client.newCall(request).execute()

                if (response.isSuccessful) {
                    val responseBody = response.body?.string()
                    if (responseBody != null) {
                        val checkResponse =
                            Request.json.decodeFromString<CheckFileResponse>(responseBody)
                        if (checkResponse.code == 200) {
                            result = checkResponse.data
                        } else {
                            Log.e("FileUpload", "检查失败: ${checkResponse.message}")
                        }
                    }
                } else {
                    Log.e("FileUpload", "HTTP错误: ${response.code} - ${response.message}")
                }
            } catch (e: Exception) {
                Log.e("FileUpload", "检查文件失败", e)
            }
        }

        return result
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

        viewModelScope.launch {
            _uploadState.value = UploadState.Uploading
            var successCount = 0
            var failCount = 0

            _selectedFiles.value.forEachIndexed { index, fileInfo ->
                try {
                    _currentUploadProgress.value = UploadProgress(
                        fileName = fileInfo.name,
                        currentIndex = index + 1,
                        totalCount = _selectedFiles.value.size,
                        progress = 0f
                    )

                    val checkResult = checkFileExists(_targetPath.value, fileInfo.name)

                    if (checkResult?.exists == true) {
                        Log.w("FileUpload", "文件已存在: ${fileInfo.name}")
                        failCount++
                    }

                    val uploaded = uploadSingleFile(fileInfo) { progress ->
                        _currentUploadProgress.value = _currentUploadProgress.value?.copy(
                            progress = progress
                        )
                    }

                    if (uploaded) {
                        successCount++
                        Log.d("FileUpload", "上传成功: ${fileInfo.name}")
                    } else {
                        failCount++
                        Log.e("FileUpload", "上传失败: ${fileInfo.name}")
                    }

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

    private suspend fun uploadSingleFile(
        fileInfo: UploadFileInfo,
        onProgress: (Float) -> Unit
    ): Boolean = withContext(Dispatchers.IO) {
        try {
            val file = File(fileInfo.path)
            if (!file.exists()) {
                Log.e("FileUpload", "文件不存在: ${fileInfo.path}")
                return@withContext false
            }

            val normalizedPath = normalizePath(_targetPath.value)
            val url = "${Request.baseUrl}/file/upload"
            val token = Request.getToken()

            Log.d("FileUpload", "上传文件: path=$normalizedPath, name=${fileInfo.name}")

            // 构建 multipart body（包含文件和参数）
            val requestBody = file.asRequestBody("application/octet-stream".toMediaTypeOrNull())
            val filePart = MultipartBody.Part.createFormData("file", file.name, requestBody)

            val multipartBody = MultipartBody.Builder()
                .setType(MultipartBody.FORM)
                .addFormDataPart("path", normalizedPath)
                .addFormDataPart("name", fileInfo.name)
                .addFormDataPart("action", "upload")
                .addPart(filePart)
                .build()

            val progressBody = ProgressRequestBody(multipartBody) { bytesWritten, contentLength ->
                val progress = bytesWritten.toFloat() / contentLength.toFloat()
                onProgress(progress)
            }

            val request = okhttp3.Request.Builder()
                .url(url)
                .apply {
                    token?.let { header("Token", it) }
                }
                .post(progressBody)
                .build()

            val response = Request.client.newCall(request).execute()

            if (response.isSuccessful) {
                val responseBody = response.body?.string()
                if (responseBody != null) {
                    val uploadResponse =
                        Request.json.decodeFromString<UploadResponse>(responseBody)
                    if (uploadResponse.code == 200) {
                        Log.d("FileUpload", "上传成功: ${uploadResponse.data}")
                        return@withContext true
                    } else {
                        Log.e("FileUpload", "上传失败: ${uploadResponse.message}")
                    }
                }
            } else {
                val errorBody = response.body?.string()
                Log.e("FileUpload", "上传失败: ${response.code} - $errorBody")
            }

            false
        } catch (e: Exception) {
            Log.e("FileUpload", "上传异常", e)
            false
        }
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

private class ProgressRequestBody(
    private val requestBody: okhttp3.RequestBody,
    private val onProgress: (bytesWritten: Long, contentLength: Long) -> Unit
) : okhttp3.RequestBody() {

    override fun contentType() = requestBody.contentType()

    override fun contentLength() = requestBody.contentLength()

    override fun writeTo(sink: BufferedSink) {
        val countingSink = CountingSink(sink, contentLength(), onProgress)
        val bufferedSink = countingSink.buffer()
        requestBody.writeTo(bufferedSink)
        bufferedSink.flush()
    }
}

private class CountingSink(
    delegate: Sink,
    private val contentLength: Long,
    private val onProgress: (bytesWritten: Long, contentLength: Long) -> Unit
) : ForwardingSink(delegate) {

    private var bytesWritten = 0L

    override fun write(source: Buffer, byteCount: Long) {
        super.write(source, byteCount)
        bytesWritten += byteCount
        onProgress(bytesWritten, contentLength)
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
    val progress: Float
)

sealed class UploadState {
    data object Idle : UploadState()
    data object Uploading : UploadState()
    data class Success(val count: Int) : UploadState()
    data class PartialSuccess(val successCount: Int, val failCount: Int) : UploadState()
    data class Error(val message: String) : UploadState()
}

@Serializable
data class CheckFileResponse(
    val code: Int = 0,
    val message: String = "",
    val data: CheckFileResult? = null
)

@Serializable
data class CheckFileResult(
    val exists: Boolean = false,
    @SerialName("can_upload")
    val canUpload: Boolean = false,
    @SerialName("file_name")
    val fileName: String = "",
    @SerialName("file_size")
    val fileSize: Long? = null,
    val path: String = ""
)

@Serializable
data class UploadResponse(
    val code: Int = 0,
    val message: String = "",
    val data: UploadResult? = null
)

@Serializable
data class UploadResult(
    @SerialName("history_id")
    val historyId: Int = 0,
    @SerialName("file_name")
    val fileName: String = "",
    @SerialName("file_size")
    val fileSize: Long = 0,
    @SerialName("storage_path")
    val storagePath: String = ""
)