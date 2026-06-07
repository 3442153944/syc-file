// api/user/UserParams.kt
// 职责：用户模块所有请求参数数据类。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag。
package com.sunyuanling.filesync.api.user

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// ==================== 登录 ====================

@Serializable
data class LoginParams(
    /** 用户名（与 email、phone 至少提供一个） */
    val username: String = "",
    /** 邮箱 */
    val email: String = "",
    /** 手机号 */
    val phone: String = "",
    /** 密码（必填） */
    val password: String
)

// ==================== 注册 ====================

@Serializable
data class RegisterParams(
    /** 用户名（与 email、phone 至少提供一个） */
    val username: String = "",
    /** 密码（必填，至少 6 位） */
    val password: String,
    /** 邮箱 */
    val email: String = "",
    /** 手机号 */
    val phone: String = "",
    /** 头像 */
    val avatar: String = ""
)

// ==================== 重置密码 ====================

@Serializable
data class ResetPasswordParams(
    /** 用户名（需至少提供三项中的两项） */
    val username: String = "",
    /** 邮箱 */
    val email: String = "",
    /** 手机号 */
    val phone: String = "",
    /** 新密码（必填，至少 6 位） */
    @SerialName("new_password")
    val newPassword: String
)

// ==================== Token 校验 ====================
// 无请求体，token 由 Request 单例自动注入 header

// ==================== 更新用户信息 ====================

/**
 * 更新用户信息请求参数。
 * 注意：此接口使用 multipart/form-data，不走 JSON body。
 * username / email / phone 以 form 字段提交，avatar 以文件字段提交。
 *
 * TODO: 当前 Request 单例不支持 multipart/form-data 请求，
 * 此接口需调用方自行构建 OkHttp MultipartBody 请求，无法通过 updateInfo() 直接调用。
 * 建议后续在 Request 单例中增加 multipart 支持。
 */
@Serializable
data class UpdateUserInfoParams(
    /** 用户名 */
    val username: String = "",
    /** 邮箱 */
    val email: String = "",
    /** 手机号 */
    val phone: String = ""
    // avatar 由调用方作为文件字段单独处理（multipart）
)
