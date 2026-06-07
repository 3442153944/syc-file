package com.sunyuanling.filesync.ui.screen.person

// 原有 @Serializable 数据类已统一迁移至 api/user/UserResponse.kt 和 api/user/UserParams.kt。
// 原有的 LoginRequest / LoginResponse / User / FullUser / VerifyResponse / UpdateUserResponse 等
// 均已由 api.user 包下的类型替代：
//   User / FullUser / PUser → api.user.UserInfo
//   LoginRequest / PLoginRequest → api.user.LoginParams
//   LoginResponse / PLoginResponse → api.user.UserApi.login() 返回 Response<LoginData>
//   VerifyResponse / PVerifyResponse → api.user.VerifyResponse
//   UpdateUserResponse / PUpdateResponse → api.user.UpdateUserInfoResponse
