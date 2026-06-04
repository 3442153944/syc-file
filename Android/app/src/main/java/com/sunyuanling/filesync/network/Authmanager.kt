package com.sunyuanling.filesync.network

// network/AuthManager.kt
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow

/**
 * 全局认证状态管理
 * 当 token 失效（401）时发送事件，UI 层监听后跳转登录页
 */
object AuthManager {

    sealed class AuthEvent {
        /** token 失效，需要重新登录 */
        data object TokenExpired : AuthEvent()
    }

    private val _authEvents = MutableSharedFlow<AuthEvent>(extraBufferCapacity = 1)
    val authEvents = _authEvents.asSharedFlow()

    fun notifyTokenExpired() {
        _authEvents.tryEmit(AuthEvent.TokenExpired)
    }
}