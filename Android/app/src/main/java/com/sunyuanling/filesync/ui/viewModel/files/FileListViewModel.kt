// ui/viewModel/files/FileListViewModel.kt
package com.sunyuanling.filesync.ui.viewModel.files

import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.network.Request
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

class FileListViewModel : ViewModel() {

    private val _fileData = MutableStateFlow<FileListData?>(null)
    val fileData: StateFlow<FileListData?> = _fileData

    private val _loading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = _loading

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    // 路径历史栈，用于返回
    private val _pathStack = MutableStateFlow<List<String>>(emptyList())
    val pathStack: StateFlow<List<String>> = _pathStack

    fun loadDirectory(path: String) {
        viewModelScope.launch {
            _loading.value = true
            _error.value = null

            try {
                Request.post<FileListResponse, TraverseRequest>(
                    "/file/traverse-directory",
                    TraverseRequest(path)
                ) { result ->
                    result.onSuccess { response ->
                        if (response.code == 200 && response.data != null) {
                            _fileData.value = response.data
                            Log.d("FileListVM", "加载成功: ${response.data.totalCount} 项")
                        } else {
                            _error.value = response.message
                        }
                    }.onFailure { e ->
                        Log.e("FileListVM", "加载目录失败", e)
                        _error.value = e.message ?: "未知错误"
                    }
                    _loading.value = false
                }
            } catch (e: Exception) {
                Log.e("FileListVM", "异常", e)
                _error.value = e.message ?: "未知错误"
                _loading.value = false
            }
        }
    }

    fun navigateTo(path: String) {
        // 保存当前路径到栈
        _fileData.value?.currentPath?.let { current ->
            _pathStack.value += current
        }
        loadDirectory(path)
    }

    fun navigateBack(): Boolean {
        val stack = _pathStack.value
        if (stack.isEmpty()) return false

        val previousPath = stack.last()
        _pathStack.value = stack.dropLast(1)
        loadDirectory(previousPath)
        return true
    }

    fun navigateToParent() {
        _fileData.value?.parentPath?.let { parent ->
            if (parent.isNotEmpty()) {
                _pathStack.value += (_fileData.value?.currentPath ?: "")
                loadDirectory(parent)
            }
        }
    }

    fun refresh() {
        _fileData.value?.currentPath?.let { loadDirectory(it) }
    }
    fun clearState() {
        _fileData.value = null
        _pathStack.value = emptyList()
        _error.value = null
        _loading.value = false
    }
}

@Serializable
data class TraverseRequest(
    val path: String
)

@Serializable
data class FileListResponse(
    val code: Int = 0,
    val message: String = "",
    val data: FileListData? = null
)

@Serializable
data class FileListData(
    @SerialName("current_path")
    val currentPath: String = "",
    @SerialName("parent_path")
    val parentPath: String = "",
    val items: List<FileItem>? =null,
    @SerialName("total_count")
    val totalCount: Int = 0,
    @SerialName("dir_count")
    val dirCount: Int = 0,
    @SerialName("file_count")
    val fileCount: Int = 0
)

@Serializable
data class FileItem(
    val name: String = "",
    val path: String = "",
    @SerialName("is_dir")
    val isDir: Boolean = false,
    val size: Long = 0,
    @SerialName("mod_time")
    val modTime: String = "",
    val mode: String = "",
    val extension: String = "",
    @SerialName("children_count")
    val childrenCount: Int = 0
)