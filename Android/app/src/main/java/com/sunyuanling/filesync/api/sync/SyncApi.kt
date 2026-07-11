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
}
