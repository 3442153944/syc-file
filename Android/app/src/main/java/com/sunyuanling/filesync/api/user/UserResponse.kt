// api/user/UserResponse.kt
// 职责：用户模块所有响应数据类。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag 及 gin.H 返回。
package com.sunyuanling.filesync.api.user

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// ==================== 通用用户信息 ====================
// 对应后端 model.User struct，复用于 login / verify 等接口的 data 中

@Serializable
data class UserInfo(
    val id: Int = 0,
    val username: String = "",
    val email: String? = null,
    val phone: String? = null,
    val avatar: String? = null,
    val role: String? = null,
    val status: Int = 1,
    @SerialName("last_login") val lastLogin: String? = null,
    @SerialName("created_at") val createdAt: String? = null
)

// ==================== 登录 ====================
// 对应后端 HandlerFuncLogin 返回格式

@Serializable
data class LoginData(
    val token: String,
    val user: UserInfo
)

// ==================== 注册 ====================
// 对应后端 HandlerFuncRegister 返回格式
//
// TODO: 后端 register 接口返回 {"message":"...", "user":{...}}，缺少 code 字段，
// 与 Request 单例内部 code==200 校验逻辑不兼容，暂时无法通过 Request.postSuspend 正常调用。
// 如未来后端修复为 {"code":200, "message":"...", "data":...} 标准格式，需同步更新此类。

@Serializable
data class RegisterResponse(
    val message: String = "",
    val user: UserInfo? = null
)

// ==================== 重置密码 ====================
// 对应后端 HandlerFuncResetPassword 返回格式

@Serializable
data class ResetPasswordResponse(
    val code: Int = 0,
    val message: String = ""
    // data 字段为 null，成功时不返回 data
)

// ==================== Token 校验 ====================
// 对应后端 HandlerFuncVerify 返回格式

@Serializable
data class VerifyResponse(
    val code: Int = 0,
    val message: String = "",
    val data: UserInfo? = null
)

// ==================== 更新用户信息 ====================
// 对应后端 HandlerFuncUpdateUserInfo 返回格式
//
// TODO: 此接口使用 multipart/form-data，当前 Request 单例不支持。

@Serializable
data class UpdateUserInfoResponse(
    val code: Int = 0,
    val message: String = ""
    // data 字段为 null，成功时不返回 data
)
