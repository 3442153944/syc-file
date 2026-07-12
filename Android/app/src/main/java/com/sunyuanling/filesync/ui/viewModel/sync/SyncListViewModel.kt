// ui/viewModel/sync/SyncListViewModel.kt
// 职责：同步列表页数据源——同步记录（分页加载）+ 待处理事项。
package com.sunyuanling.filesync.ui.viewModel.sync

import android.app.Application
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.sunyuanling.filesync.api.sync.SyncApi
import com.sunyuanling.filesync.api.sync.SyncConflictInfo
import com.sunyuanling.filesync.api.sync.SyncTaskInfo
import com.sunyuanling.filesync.sync.SyncEngine
import com.sunyuanling.filesync.util.DeviceInfoUtil
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class SyncListUiState(
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    val error: String? = null,
    /** 同步记录（分页追加） */
    val records: List<SyncTaskInfo> = emptyList(),
    val hasMore: Boolean = false,
    val totalRecords: Long = 0,
    /** 本设备待执行任务 */
    val pendingTasks: List<SyncTaskInfo> = emptyList(),
    /** 待处理冲突 */
    val conflicts: List<SyncConflictInfo> = emptyList(),
    /** 正在操作中的冲突 id（防重复点击） */
    val busyConflictIds: Set<Long> = emptySet(),
)

class SyncListViewModel(app: Application) : AndroidViewModel(app) {

    private val _uiState = MutableStateFlow(SyncListUiState())
    val uiState: StateFlow<SyncListUiState> = _uiState.asStateFlow()

    private val deviceId: String by lazy {
        DeviceInfoUtil.getDeviceId(getApplication())
    }

    private var currentPage = 0
    private val pageSize = 10

    fun refresh() {
        if (_uiState.value.loading) return
        currentPage = 0
        _uiState.value = _uiState.value.copy(loading = true, error = null, records = emptyList(), hasMore = false)
        viewModelScope.launch {
            val recordsDeferred = async { SyncApi.listTasksPaged(page = 1, pageSize = pageSize) }
            val pendingDeferred = async { SyncApi.pendingTasks(deviceId) }
            val conflictsDeferred = async { SyncApi.listConflicts() }

            val records = recordsDeferred.await()
            val pending = pendingDeferred.await()
            val conflicts = conflictsDeferred.await()

            val firstError = listOf(records, pending, conflicts)
                .firstOrNull { it.isFailure }?.exceptionOrNull()?.message

            val page = records.getOrNull()?.data
            currentPage = 1
            _uiState.value = _uiState.value.copy(
                loading = false,
                error = firstError,
                records = page?.list.orEmpty(),
                hasMore = page != null && page.list.size >= pageSize,
                totalRecords = page?.total ?: 0,
                pendingTasks = pending.getOrNull()?.data.orEmpty(),
                conflicts = conflicts.getOrNull()?.data.orEmpty(),
            )
            if (firstError != null) Log.w(TAG, "同步列表刷新部分失败: $firstError")
        }
    }

    /** 加载下一页（追加到现有列表尾部）。 */
    fun loadMore() {
        val s = _uiState.value
        if (s.loadingMore || !s.hasMore) return
        val nextPage = currentPage + 1
        _uiState.value = s.copy(loadingMore = true)
        viewModelScope.launch {
            SyncApi.listTasksPaged(page = nextPage, pageSize = pageSize)
                .onSuccess { resp ->
                    if (resp.code == 200 && resp.data != null) {
                        val page = resp.data
                        currentPage = nextPage
                        _uiState.value = _uiState.value.copy(
                            loadingMore = false,
                            records = _uiState.value.records + page.list,
                            hasMore = page.list.size >= pageSize,
                        )
                    } else {
                        _uiState.value = _uiState.value.copy(loadingMore = false, hasMore = false)
                    }
                }
                .onFailure { e ->
                    _uiState.value = _uiState.value.copy(loadingMore = false, error = "加载失败: ${e.message}")
                }
        }
    }

    fun resolveConflict(id: Long, resolution: String) {
        markBusy(id, true)
        viewModelScope.launch {
            SyncApi.resolveConflict(id, resolution)
                .onSuccess {
                    _uiState.value = _uiState.value.copy(
                        conflicts = _uiState.value.conflicts.filterNot { it.id == id }
                    )
                }
                .onFailure { e ->
                    _uiState.value = _uiState.value.copy(error = "解决冲突失败: ${e.message}")
                }
            markBusy(id, false)
        }
    }

    fun resolveAllConflicts(resolution: String) {
        val targets = _uiState.value.conflicts.map { it.id }
        if (targets.isEmpty()) return
        _uiState.value = _uiState.value.copy(busyConflictIds = targets.toSet())
        viewModelScope.launch {
            var failed = 0
            for (id in targets) {
                SyncApi.resolveConflict(id, resolution).onFailure { failed++ }
            }
            _uiState.value = _uiState.value.copy(
                busyConflictIds = emptySet(),
                error = if (failed > 0) "有 $failed 条冲突处理失败" else null,
            )
            refresh()
        }
    }

    fun realign() { SyncEngine.triggerCatchUp() }

    fun deleteConflict(id: Long) {
        markBusy(id, true)
        viewModelScope.launch {
            SyncApi.deleteConflict(id)
                .onSuccess {
                    _uiState.value = _uiState.value.copy(
                        conflicts = _uiState.value.conflicts.filterNot { it.id == id }
                    )
                }
                .onFailure { e ->
                    _uiState.value = _uiState.value.copy(error = "删除冲突失败: ${e.message}")
                }
            markBusy(id, false)
        }
    }

    private fun markBusy(id: Long, busy: Boolean) {
        val ids = _uiState.value.busyConflictIds.toMutableSet()
        if (busy) ids.add(id) else ids.remove(id)
        _uiState.value = _uiState.value.copy(busyConflictIds = ids)
    }

    companion object {
        private const val TAG = "SyncListVM"

        fun statusLabel(status: String): String = when (status) {
            "pending" -> "待处理"
            "syncing" -> "同步中"
            "completed" -> "已完成"
            "failed" -> "失败"
            "blocked", "waiting_unlock" -> "等待解锁"
            else -> status.ifEmpty { "未知" }
        }

        fun typeLabel(type: String): String = when (type) {
            "download" -> "下载"
            "upload" -> "上传"
            "delete" -> "删除"
            "mkdir" -> "建目录"
            else -> type.ifEmpty { "未知" }
        }
    }
}
