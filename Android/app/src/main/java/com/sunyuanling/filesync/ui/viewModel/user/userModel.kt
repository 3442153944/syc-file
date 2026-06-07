package com.sunyuanling.filesync.ui.viewModel.user

import com.sunyuanling.filesync.api.user.UserInfo

// ==================== UI 状态 ====================

sealed class PPersonalState {
    data object Loading : PPersonalState()
    data object NotLoggedIn : PPersonalState()
    data class LoggedIn(val user: UserInfo) : PPersonalState()
    data class Error(val message: String) : PPersonalState()
}
