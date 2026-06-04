package com.sunyuanling.filesync.ui.screen.person

import kotlinx.serialization.Serializable

@Serializable
data class VerifyResponse(
    val code: Int,
    val message: String,
    val data: VerifyData
)

@Serializable
data class VerifyData(
    val msg: String,
    val userInfo: FullUser
)

@Serializable
data class UserInfo(
    val user_id: Int,
    val username: String,
    val email: String?,
    val issued_at: Long,
    val expires_at: Long
)

@Serializable
data class LoginRequest(
    val username: String,
    val password: String
)

@Serializable
data class LoginResponse(
    val code: Int,
    val message: String,
    val data: LoginData
)

@Serializable
data class LoginData(
    val token: String,
    val user: User
)

@Serializable
data class User(
    val avatar: String? = null,
    val email: String? = null,
    val id: Int,
    val last_login: String? = null,
    val phone: String? = null,
    val role: String? = null,
    val status: Int,
    val username: String
)

// ==================== 统一用户模型 ====================

@Serializable
data class FullUser(
    val id: Int,
    val username: String,
    val email: String? = null,
    val phone: String? = null,
    val avatar: String? = null,
    val role: String? = null,
    val status: Int = 1,
    val last_login: String? = null,
    val created_at: String? = null
)

// ==================== Update ====================

@Serializable
data class UpdateUserResponse(
    val code: Int,
    val message: String,
    val data: UpdateUserData? = null
)

@Serializable
data class UpdateUserData(
    val message: String = ""
)
fun UserInfo.toFullUser() = FullUser(
    id = user_id,
    username = username,
    email = email
)

fun FullUser.toUserInfo() = UserInfo(
    user_id = id,
    username = username,
    email = email,
    issued_at = 0,
    expires_at = 0
)