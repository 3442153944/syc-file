// ui/viewModel/sync/SyncFolderMapViewModel.kt
// 职责：同步文件夹映射页数据源——服务器 folder 列表（Windows 端创建）+ 本设备映射。
package com.sunyuanling.filesync.ui.viewModel.sync

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.sync.SyncApi
import com.sunyuanling.filesync.api.sync.SyncFolderInfo
import com.sunyuanling.filesync.sync.SyncEngine
import com.sunyuanling.filesync.sync.SyncMapping
import com.sunyuanling.filesync.sync.SyncMappingStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class FolderMapUiState(
    val loading: Boolean = false,
    val error: String? = null,
    val folders: List<SyncFolderInfo> = emptyList(),
)

class SyncFolderMapViewModel : ViewModel() {

    private val _uiState = MutableStateFlow(FolderMapUiState())
    val uiState: StateFlow<FolderMapUiState> = _uiState.asStateFlow()

    /** 本设备映射（folderId → SyncMapping），直接暴露 Store 的流。 */
    val mappings = SyncMappingStore.mappings

    init {
        SyncMappingStore.load()
    }

    fun refresh() {
        if (_uiState.value.loading) return
        _uiState.value = _uiState.value.copy(loading = true, error = null)
        viewModelScope.launch {
            SyncApi.listFolders()
                .onSuccess { resp ->
                    _uiState.value = _uiState.value.copy(
                        loading = false, folders = resp.data.orEmpty()
                    )
                }
                .onFailure { e ->
                    _uiState.value = _uiState.value.copy(loading = false, error = e.message)
                }
        }
    }

    /** 保存映射并通知引擎重建监听 + 触发追赶。 */
    fun setMapping(folderId: Long, localPath: String, enabled: Boolean) {
        SyncMappingStore.put(SyncMapping(folderId = folderId, localPath = localPath, enabled = enabled))
        SyncEngine.onMappingsChanged(clearedFolderId = folderId)
    }

    fun removeMapping(folderId: Long) {
        SyncMappingStore.remove(folderId)
        SyncEngine.onMappingsChanged(clearedFolderId = folderId)
    }

    fun defaultPathFor(folder: SyncFolderInfo): String =
        SyncMappingStore.defaultLocalPath(folder.name, folder.id)
}
