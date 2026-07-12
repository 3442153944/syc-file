// api/file/FileApi.kt
// 职责：文件相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 Request.xSuspend，不包含业务逻辑。
// buildDownloadUrl() 是例外：token 无法通过 header 携带，故拼入 query string。
package com.sunyuanling.filesync.api.file

import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response as ApiResponse
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request as OkRequest
import okhttp3.RequestBody.Companion.toRequestBody
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

    // ==================== 分片上传 ====================

    /** 初始化分片上传：提交描述信息，返回 upload_id + 缺失分片（instant=true 为秒传已完成）。 */
    suspend fun uploadInit(params: UploadInitParams): Result<ApiResponse<UploadInitData>> {
        return Request.postSuspend<ApiResponse<UploadInitData>, UploadInitParams>(
            ApiRoutes.FILE_UPLOAD_INIT, params
        )
    }

    /** 查询上传进度/缺失分片（断点续传）。 */
    suspend fun uploadStatus(uploadId: String): Result<ApiResponse<UploadStatusData>> {
        return Request.getSuspend<ApiResponse<UploadStatusData>>(
            ApiRoutes.FILE_UPLOAD_STATUS, mapOf("upload_id" to uploadId)
        )
    }

    /** 完成分片上传：收齐后触发服务端校验落盘。 */
    suspend fun uploadComplete(params: UploadCompleteParams): Result<ApiResponse<UploadCompleteData>> {
        return Request.postSuspend<ApiResponse<UploadCompleteData>, UploadCompleteParams>(
            ApiRoutes.FILE_UPLOAD_COMPLETE, params
        )
    }

    /**
     * 上传单个分片：query 带 upload_id+index，body 是分片裸字节（application/octet-stream）。
     * Request 单例只支持 JSON，故这里直接用 OkHttp。
     *  - 422 → ChunkVerifyException（该片校验失败，调用方重传该片）
     *  - 404 → SessionGoneException（会话过期，调用方重新 init）
     */
    suspend fun uploadChunk(uploadId: String, index: Int, data: ByteArray): Result<UploadChunkData> =
        withContext(Dispatchers.IO) {
            try {
                val url = "${Request.baseUrl}${ApiRoutes.FILE_UPLOAD_CHUNK}" +
                        "?upload_id=${URLEncoder.encode(uploadId, "UTF-8")}&index=$index"
                val token = Request.getToken()
                val body = data.toRequestBody("application/octet-stream".toMediaType())
                val builder = OkRequest.Builder().url(url).post(body)
                token?.let { builder.header("Token", it) }

                Request.client.newCall(builder.build()).execute().use { resp ->
                    val text = resp.body?.string().orEmpty()
                    if (!resp.isSuccessful) {
                        return@withContext Result.failure(Exception("HTTP ${resp.code}: ${resp.message}"))
                    }
                    val obj = Request.json.parseToJsonElement(text).jsonObject
                    val code = obj["code"]?.jsonPrimitive?.intOrNull ?: -1
                    val message = obj["message"]?.jsonPrimitive?.content ?: "未知错误"
                    when (code) {
                        200 -> {
                            val parsed = Request.json.decodeFromString<ApiResponse<UploadChunkData>>(text)
                            Result.success(parsed.data ?: UploadChunkData())
                        }
                        422 -> Result.failure(ChunkVerifyException(index, message))
                        404 -> Result.failure(SessionGoneException(message))
                        else -> Result.failure(Exception(message))
                    }
                }
            } catch (e: Exception) {
                Result.failure(e)
            }
        }

    // ==================== 删除文件 ====================

    /**
     * 删除远端单个文件（拒绝目录）。
     * 同步覆盖上传前置：服务端 init 拒绝已存在文件，先删旧再传新（delete-before-upload）。
     * 文件不存在（404）对调用方视为可忽略的失败。
     */
    suspend fun deleteFile(params: DeleteFileParams): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, DeleteFileParams>(
            ApiRoutes.FILE_DELETE, params
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
