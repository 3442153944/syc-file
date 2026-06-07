package com.sunyuanling.filesync.ui.screen.person

import com.sunyuanling.filesync.api.user.UserInfo

sealed class PersonalUiState {
    object Loading : PersonalUiState()
    object NotLoggedIn : PersonalUiState()
    data class LoggedIn(val userInfo: UserInfo) : PersonalUiState()
    data class Error(val message: String) : PersonalUiState()
}