// api/update/UpdateApi.kt
// 应用更新 API 门面。只组装参数 + 调 Request.xSuspend。
package com.sunyuanling.filesync.api.update

import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response as ApiResponse

object UpdateApi {

    /** 检查更新：传入本机 version_code，返回是否有新版本。 */
    suspend fun check(
        versionCode: Long,
        platform: String = "android"
    ): Result<ApiResponse<CheckResult>> {
        return Request.getSuspend<ApiResponse<CheckResult>>(
            ApiRoutes.UPDATE_CHECK,
            mapOf("platform" to platform, "version_code" to versionCode.toString())
        )
    }

    /** 最新上架版本（可用于"检查更新"按钮的手动查询）。data 可能为 null（无发布）。 */
    suspend fun latest(platform: String = "android"): Result<ApiResponse<AppReleaseDto>> {
        return Request.getSuspend<ApiResponse<AppReleaseDto>>(
            ApiRoutes.UPDATE_LATEST,
            mapOf("platform" to platform)
        )
    }
}
