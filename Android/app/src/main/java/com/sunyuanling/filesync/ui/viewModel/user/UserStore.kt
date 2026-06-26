package com.sunyuanling.filesync.ui.viewModel.user

import com.sunyuanling.filesync.api.user.UserInfo
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * 当前用户全局可观察存储（进程级单例）。
 * 提供 role/admin 判定的唯一可信来源，供监控页等模块判断是否展示管理员功能。
 * 由登录成功 / verify 成功 时填充；登出时清除。
 */
object UserStore {

    private val _userInfo = MutableStateFlow<UserInfo?>(null)
    val userInfo: StateFlow<UserInfo?> = _userInfo.asStateFlow()

    val current: UserInfo? get() = _userInfo.value

    val isAdmin: Boolean
        get() = _userInfo.value?.role?.equals("admin", ignoreCase = true) == true

    fun setCurrent(user: UserInfo) {
        _userInfo.value = user
    }

    fun clear() {
        _userInfo.value = null
    }
}
