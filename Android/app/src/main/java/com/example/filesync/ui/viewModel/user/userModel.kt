package com.example.filesync.ui.viewModel.user

import kotlinx.serialization.Serializable

// ==================== 用户模型 ====================

@Serializable
data class PUser(
    val id: Int = 0,
    val username: String = "",
    val email: String? = null,
    val phone: String? = null,
    val avatar: String? = null,
    val role: String? = null,
    val status: Int = 1,
    val last_login: String? = null,
    val created_at: String? = null
)

// ==================== 登录 ====================

@Serializable
data class PLoginRequest(
    val username: String,
    val password: String
)

@Serializable
data class PLoginResponse(
    val code: Int,
    val message: String,
    val data: PLoginData
)

@Serializable
data class PLoginData(
    val token: String,
    val user: PUser
)

// ==================== Token 校验 ====================

@Serializable
data class PVerifyResponse(
    val code: Int,
    val message: String,
    val data: PUser
)

// ==================== 更新用户信息 ====================

@Serializable
data class PUpdateResponse(
    val code: Int,
    val message: String,
    val data: PUpdateData? = null
)

@Serializable
data class PUpdateData(
    val message: String = ""
)

// ==================== UI 状态 ====================

sealed class PPersonalState {
    data object Loading : PPersonalState()
    data object NotLoggedIn : PPersonalState()
    data class LoggedIn(val user: PUser) : PPersonalState()
    data class Error(val message: String) : PPersonalState()

}
