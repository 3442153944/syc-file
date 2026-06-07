// api/user/UserApi.kt
// 职责：用户相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 Request.xSuspend，不包含业务逻辑。
package com.sunyuanling.filesync.api.user

import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response as ApiResponse

object UserApi {

    /**
     * 用户登录。
     * 成功后 Request 单例自动提取并保存 token（白名单匹配 /user/login）。
     */
    suspend fun login(params: LoginParams): Result<ApiResponse<LoginData>> {
        return Request.postSuspend<ApiResponse<LoginData>, LoginParams>(
            ApiRoutes.USER_LOGIN, params
        )
    }

    /**
     * 用户注册。
     *
     * TODO: 后端 register 返回 {"message":"...","user":{...}} 缺少 code 字段，
     * 与 Request 单例的 code==200 检查不兼容，此函数目前无法正常工作。
     * 待后端修复为标准 {code, message, data} 格式后，将返回类型改为 ApiResponse<...>。
     */
    suspend fun register(params: RegisterParams): Result<RegisterResponse> {
        return Request.postSuspend<RegisterResponse, RegisterParams>(
            ApiRoutes.USER_REGISTER, params
        )
    }

    /**
     * 重置密码。
     */
    suspend fun resetPassword(params: ResetPasswordParams): Result<ResetPasswordResponse> {
        return Request.postSuspend<ResetPasswordResponse, ResetPasswordParams>(
            ApiRoutes.USER_RESET_PASSWORD, params
        )
    }

    /**
     * 校验 Token 有效性。
     * 无请求体，token 由 Request 单例自动注入 header。
     * 成功后 Request 单例自动提取并保存 token（白名单匹配 /user/verify）。
     */
    suspend fun verify(): Result<VerifyResponse> {
        return Request.postSuspend<VerifyResponse>(ApiRoutes.USER_VERIFY)
    }

    /**
     * 更新用户信息。
     *
     * TODO: 后端此接口使用 multipart/form-data（form 标签绑定的字段 + 可选的 avatar 文件），
     * 而非 JSON body。当前 Request 单例只发送 JSON，不支持 multipart。
     * 调用方需自行构造 OkHttp MultipartBody 请求，暂无法通过此函数调用。
     * 待 Request 单例增加 multipart 支持后再接入。
     */
    suspend fun updateInfo(params: UpdateUserInfoParams): Result<UpdateUserInfoResponse> {
        return Request.postSuspend<UpdateUserInfoResponse, UpdateUserInfoParams>(
            ApiRoutes.USER_UPDATE_INFO, params
        )
    }
}
