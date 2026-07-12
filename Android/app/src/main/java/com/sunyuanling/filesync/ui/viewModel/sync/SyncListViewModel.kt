// ui/viewModel/sync/SyncListViewModel.kt
// 职责：同步列表页数据源——同步记录（/sync/tasks）+ 待处理事项
// （本设备待执行任务 /sync/tasks/pending + 冲突 /sync/conflicts），
// 以及冲突的 resolve / delete 操作。
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
    val error: String? = null,
    /** 同步记录（全部状态，时间倒序由后端返回顺序决定） */
    val records: List<SyncTaskInfo> = emptyList(),
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

    fun refresh() {
        if (_uiState.value.loading) return
        _uiState.value = _uiState.value.copy(loading = true, error = null)
        viewModelScope.launch {
            val recordsDeferred = async { SyncApi.listTasks(limit = 200) }
            val pendingDeferred = async { SyncApi.pendingTasks(deviceId) }
            val conflictsDeferred = async { SyncApi.listConflicts() }

            val records = recordsDeferred.await()
            val pending = pendingDeferred.await()
            val conflicts = conflictsDeferred.await()

            val firstError = listOf(records, pending, conflicts)
                .firstOrNull { it.isFailure }?.exceptionOrNull()?.message

            _uiState.value = _uiState.value.copy(
                loading = false,
                error = firstError,
                records = records.getOrNull()?.data.orEmpty(),
                pendingTasks = pending.getOrNull()?.data.orEmpty(),
                conflicts = conflicts.getOrNull()?.data.orEmpty(),
            )
            if (firstError != null) Log.w(TAG, "同步列表刷新部分失败: $firstError")
        }
    }

    /** 解决冲突：resolution = accept_server / keep_local，成功后从列表移除。 */
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

    /**
     * 批量解决全部待处理冲突（以服务器为准的两种取舍）：
     * - accept_server：放弃本地更改，主目录收敛服务端版本（强制对齐）；
     * - keep_local  ：本地更改已暂存 .syncpending，以服务端当前版本为 base 重新上报。
     */
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

    /** 与服务器重新对齐：触发引擎一轮追赶（执行积压任务 + scan 比对补齐）。 */
    fun realign() {
        SyncEngine.triggerCatchUp()
    }

    /** 删除冲突记录（不做取舍，仅清理残留）。 */
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

        /** sync_status → 中文标签 */
        fun statusLabel(status: String): String = when (status) {
            "pending" -> "待处理"
            "syncing" -> "同步中"
            "completed" -> "已完成"
            "failed" -> "失败"
            "blocked", "waiting_unlock" -> "等待解锁"
            else -> status.ifEmpty { "未知" }
        }

        /** task_type → 中文标签 */
        fun typeLabel(type: String): String = when (type) {
            "download" -> "下载"
            "upload" -> "上传"
            "delete" -> "删除"
            "mkdir" -> "建目录"
            else -> type.ifEmpty { "未知" }
        }
    }
}
