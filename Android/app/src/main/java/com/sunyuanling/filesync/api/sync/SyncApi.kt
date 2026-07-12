// api/sync/SyncApi.kt
// 职责：同步域 API 调用封装，对应后端 /v1/sync/*（new_server/internal/sync/router.go）。
// 每个函数只做：组装参数 + 调用 Request.xSuspend，不包含业务逻辑。
package com.sunyuanling.filesync.api.sync

import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response as ApiResponse

object SyncApi {

    /**
     * 同步任务记录（默认全部状态，倒序由后端决定）。
     * @param status  过滤状态（pending/syncing/completed/failed…），空串不过滤
     * @param deviceId 过滤目标设备，空串不过滤
     * @param limit   条数上限
     */
    suspend fun listTasks(
        status: String = "",
        deviceId: String = "",
        limit: Int = 100,
    ): Result<ApiResponse<List<SyncTaskInfo>?>> {
        val query = buildMap {
            if (status.isNotEmpty()) put("status", status)
            if (deviceId.isNotEmpty()) put("device_id", deviceId)
            if (limit > 0) put("limit", limit.toString())
        }
        return Request.getSuspend<ApiResponse<List<SyncTaskInfo>?>>(ApiRoutes.SYNC_TASKS, query)
    }

    /** 分页查询同步任务（虚拟滚动用）。 */
    data class SyncTaskPage(
        val list: List<SyncTaskInfo> = emptyList(),
        val total: Long = 0,
        val page: Int = 0,
        val pageSize: Int = 0,
    )

    @kotlinx.serialization.Serializable
    private data class SyncTaskPageRaw(
        val list: List<SyncTaskInfo> = emptyList(),
        val total: Long = 0,
        val page: Int = 0,
        @kotlinx.serialization.SerialName("page_size")
        val pageSize: Int = 0,
    )

    suspend fun listTasksPaged(
        page: Int = 1,
        pageSize: Int = 10,
        status: String = "",
        deviceId: String = "",
    ): Result<ApiResponse<SyncTaskPage>> {
        val query = buildMap {
            put("page", page.toString())
            put("page_size", pageSize.toString())
            if (status.isNotEmpty()) put("status", status)
            if (deviceId.isNotEmpty()) put("device_id", deviceId)
        }
        return Request.getSuspend<ApiResponse<SyncTaskPageRaw>>(ApiRoutes.SYNC_TASKS, query).map { raw ->
            ApiResponse(raw.code, raw.message, raw.data?.let {
                SyncTaskPage(it.list, it.total, it.page, it.pageSize)
            })
        }
    }

    /** 本设备的待执行任务（后端按 target_device_id 过滤 pending 态）。 */
    suspend fun pendingTasks(deviceId: String): Result<ApiResponse<List<SyncTaskInfo>?>> {
        return Request.getSuspend<ApiResponse<List<SyncTaskInfo>?>>(
            ApiRoutes.SYNC_TASKS_PENDING, mapOf("device_id" to deviceId)
        )
    }

    /** 当前用户的待处理冲突列表。 */
    suspend fun listConflicts(): Result<ApiResponse<List<SyncConflictInfo>?>> {
        return Request.getSuspend<ApiResponse<List<SyncConflictInfo>?>>(ApiRoutes.SYNC_CONFLICTS)
    }

    /** 解决冲突：resolution = accept_server / keep_local。 */
    suspend fun resolveConflict(id: Long, resolution: String): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, ResolveConflictParams>(
            ApiRoutes.SYNC_CONFLICT_RESOLVE.format(id.toString()),
            ResolveConflictParams(resolution)
        )
    }

    /** 删除冲突记录（残留清理）。 */
    suspend fun deleteConflict(id: Long): Result<ApiResponse<Unit?>> {
        return Request.requestSuspend<ApiResponse<Unit?>, Unit>(
            "DELETE", ApiRoutes.SYNC_CONFLICT_DELETE.format(id.toString()), null, null
        )
    }

    /** 同步文件夹列表。 */
    suspend fun listFolders(): Result<ApiResponse<List<SyncFolderInfo>?>> {
        return Request.getSuspend<ApiResponse<List<SyncFolderInfo>?>>(ApiRoutes.SYNC_FOLDERS)
    }

    // ==================== 探测上报（客户端 → 服务端） ====================

    /** 上报单文件变更（必须先上传成功；带 base_hash 供服务端 CAS）。 */
    suspend fun notify(params: SyncNotifyParams): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, SyncNotifyParams>(ApiRoutes.SYNC_NOTIFY, params)
    }

    /** 上报全量扫描清单，服务端与 trunk 比对后补派任务（离线追赶）。 */
    suspend fun scan(params: SyncScanParams): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, SyncScanParams>(ApiRoutes.SYNC_SCAN, params)
    }

    // ==================== 任务执行回报 ====================

    /** 任务完成（download 回传落盘 hash 供服务端校验）。 */
    suspend fun completeTask(id: Long, fileHash: String = ""): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, TaskCompleteParams>(
            ApiRoutes.SYNC_TASK_COMPLETE.format(id.toString()), TaskCompleteParams(fileHash)
        )
    }

    /** 任务失败（计入重试次数，由服务端 Reaper 重派）。 */
    suspend fun failTask(id: Long, error: String): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, TaskFailedParams>(
            ApiRoutes.SYNC_TASK_FAILED.format(id.toString()), TaskFailedParams(error)
        )
    }

    /** 目标文件被占用：转 waiting_unlock，不计入重试次数。 */
    suspend fun blockTask(id: Long, reason: String): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, TaskBlockedParams>(
            ApiRoutes.SYNC_TASK_BLOCKED.format(id.toString()), TaskBlockedParams(reason)
        )
    }
}
