// api/file/FileApi.kt
// 职责：文件相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 Request.xSuspend，不包含业务逻辑。
// buildDownloadUrl() 是例外：token 无法通过 header 携带，故拼入 query string。
package com.sunyuanling.filesync.api.file

import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response as ApiResponse
import java.net.URLEncoder

object FileApi {

    /**
     * 获取可用磁盘列表。
     */
    suspend fun getAvailableDisks(params: AvailableDisksParams = AvailableDisksParams()): Result<ApiResponse<AvailableDisksData>> {
        return Request.postSuspend<ApiResponse<AvailableDisksData>, AvailableDisksParams>(
            ApiRoutes.FILE_AVAILABLE_DISKS, params
        )
    }

    /**
     * 遍历目录。
     */
    suspend fun traverseDirectory(params: TraverseDirectoryParams): Result<ApiResponse<TraverseDirectoryData>> {
        return Request.postSuspend<ApiResponse<TraverseDirectoryData>, TraverseDirectoryParams>(
            ApiRoutes.FILE_TRAVERSE_DIRECTORY, params
        )
    }

    // ==================== 下载 ====================

    /**
     * 构建完整下载 URL，将 token 拼入 query string。
     * 此场景无法通过 header 携带 token（例如使用 PRDownloader 等第三方库直接下载），
     * 故将 token 作为 query 参数传递。
     *
     * @return 完整 URL，如 https://ddns.sunyuanling.cn:8891/v1/file/download?path=...&name=...&token=...
     */
    suspend fun buildDownloadUrl(params: DownloadParams): String {
        val base = "${AppConfig.getBaseUrl()}/v1${ApiRoutes.FILE_DOWNLOAD}"
        val token = Request.getToken()
        val queryMap = mutableMapOf<String, String>()
        queryMap["path"] = params.path
        queryMap["name"] = params.name
        if (params.deviceId.isNotEmpty()) {
            queryMap["device_id"] = params.deviceId
        }
        if (!token.isNullOrEmpty()) {
            queryMap["token"] = token
        }
        val queryString = queryMap.entries.joinToString("&") { (k, v) ->
            "$k=${URLEncoder.encode(v, "UTF-8")}"
        }
        return "$base?$queryString"
    }

    /**
     * 触发文件下载（GET 请求）。
     *
     * TODO: 后端 /v1/file/download 返回的是文件流而非 JSON，Request 单例期望 JSON 响应，
     * 无法直接用于下载。调用方应使用 buildDownloadUrl() 获取完整 URL 后通过 OkHttp 或
     * PRDownloader 直接发起流式下载。
     */
    suspend fun download(params: DownloadParams): Result<ApiResponse<Unit?>> {
        val queryMap = mutableMapOf<String, String>()
        queryMap["path"] = params.path
        queryMap["name"] = params.name
        if (params.deviceId.isNotEmpty()) {
            queryMap["device_id"] = params.deviceId
        }
        return Request.getSuspend<ApiResponse<Unit?>>(ApiRoutes.FILE_DOWNLOAD, queryMap)
    }

    // ==================== 上传 ====================

    /**
     * 检查文件是否已存在（JSON body，action=check）。
     */
    suspend fun checkFile(params: UploadParams): Result<ApiResponse<CheckFileData>> {
        return Request.postSuspend<ApiResponse<CheckFileData>, UploadParams>(
            ApiRoutes.FILE_UPLOAD, params
        )
    }

    /**
     * 上传文件。
     *
     * TODO: 后端 action=upload 时要求 multipart/form-data（path/name/action 作为 form 字段，
     * 文件作为 multipart 字段），当前 Request 单例不支持 multipart 请求。
     * 调用方需自行构造 OkHttp MultipartBody 发起请求。
     * 待 Request 单例增加 multipart 支持后再接入。
     */
    suspend fun uploadFile(params: UploadParams): Result<ApiResponse<UploadData>> {
        return Request.postSuspend<ApiResponse<UploadData>, UploadParams>(
            ApiRoutes.FILE_UPLOAD, params
        )
    }

    // ==================== 下载历史 ====================

    /**
     * 获取下载历史记录（分页）。
     */
    suspend fun getDownloadHistory(params: DownloadHistoryParams): Result<ApiResponse<DownloadHistoryData>> {
        return Request.postSuspend<ApiResponse<DownloadHistoryData>, DownloadHistoryParams>(
            ApiRoutes.FILE_DOWNLOAD_HISTORY, params
        )
    }

    /**
     * 删除下载记录ids
     * */
    suspend fun deleteDownloadHistory(parms: DeleteDownloadHistoryParams): Result<ApiResponse<DeleteDownloadHistoryResponse>> {
        return Request.postSuspend<ApiResponse<DeleteDownloadHistoryResponse>, DeleteDownloadHistoryParams>(
            ApiRoutes.FILE_DELETE_DOWNLOAD_HISTORY,
            parms
        )
    }
}
