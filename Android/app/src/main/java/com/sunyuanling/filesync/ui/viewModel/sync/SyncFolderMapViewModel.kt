// ui/viewModel/sync/SyncFolderMapViewModel.kt
// 职责：同步文件夹映射页数据源——服务器唯一 folder（Windows 端创建）+ 本设备映射。
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
import java.io.File

data class FolderMapUiState(
    val loading: Boolean = false,
    val error: String? = null,
    val folder: SyncFolderInfo? = null,
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
            SyncApi.getFolder()
                .onSuccess { resp ->
                    _uiState.value = _uiState.value.copy(loading = false, folder = resp.data)
                }
                .onFailure { e ->
                    _uiState.value = _uiState.value.copy(loading = false, error = e.message)
                }
        }
    }

    /** 保存映射并通知引擎重建监听 + 触发追赶。
     * 本地目录立即创建，不等引擎下一轮 catchUp/rebuildWatchers 才建——用户点"设置"这个
     * 动作本身就该在文件管理器里看得到目录，不然感觉像是没生效。
     * 只有本地路径真的变了才清基线：重新保存同一个路径（比如仅仅切一下启用开关、或者
     * 用户重复点了一次"设置"）不该把一份跟磁盘内容本来对得上的基线整个清空——否则
     * catchUpFolder 会把所有已经下载好的文件当成"从没见过的本地新文件"批量往回传，
     * 打乒乓。 */
    fun setMapping(folderId: Long, localPath: String, enabled: Boolean) {
        File(localPath).mkdirs()
        val pathChanged = SyncMappingStore.mappingFor(folderId)?.localPath != localPath
        SyncMappingStore.put(SyncMapping(folderId = folderId, localPath = localPath, enabled = enabled))
        SyncEngine.onMappingsChanged(clearedFolderId = folderId.takeIf { pathChanged })
    }

    fun removeMapping(folderId: Long) {
        SyncMappingStore.remove(folderId)
        SyncEngine.onMappingsChanged(clearedFolderId = folderId)
    }

    fun defaultPathFor(folder: SyncFolderInfo): String =
        SyncMappingStore.defaultLocalPath(folder.name, folder.id)

    /** 按默认路径映射并启用（未映射提示弹窗的"一键映射"）。 */
    fun mapDefault(folder: SyncFolderInfo) {
        setMapping(folder.id, defaultPathFor(folder), enabled = true)
    }
}
