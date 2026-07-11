package com.sunyuanling.filesync.service

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.data.sync.WsState
import com.sunyuanling.filesync.MainActivity
import com.sunyuanling.filesync.network.Request
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * 同步保活前台服务（强制保活开关的消费者）。
 *
 * 职责：常驻低优先级通知 + 持有 WebSocket 连接——app 退到后台后仍在线，
 * 可实时接收同步任务派发（task_created 等）。守护策略：
 * - 监视循环每 [CHECK_INTERVAL_MS] 检查连接，掉线且有 token 就重连
 *   （兜住 WebSocketManager 达到重连上限后放弃的情形）；
 * - START_STICKY：被系统杀死后尽力重启（厂商 ROM 不保证，root 设备可由
 *   LSPosed 看门狗模块外部拉起）。
 *
 * 与 Activity 生命周期的关系：开关开启时 MainActivity 的 ON_STOP 不再断开 WS，
 * 连接归本服务管；开关关闭则一切回到"前台才在线"的旧行为。
 */
class SyncKeepAliveService : Service() {

    companion object {
        const val FG_NOTIF_ID = 9002
        private const val CHANNEL_ID = "sync_keepalive"
        private const val CHECK_INTERVAL_MS = 30_000L

        fun start(context: Context) {
            ContextCompat.startForegroundService(
                context, Intent(context, SyncKeepAliveService::class.java)
            )
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, SyncKeepAliveService::class.java))
        }
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onCreate() {
        super.onCreate()
        ensureChannel()
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
        ServiceCompat.startForeground(this, FG_NOTIF_ID, buildNotification("同步保活运行中"), type)

        // 连接状态 → 通知文案
        scope.launch {
            WebSocketManager.connectionState.collect { state ->
                val text = when (state) {
                    is WsState.Connected -> "已连接，实时同步在线"
                    is WsState.Connecting -> "连接中…"
                    is WsState.Disconnected -> "已断开，等待重连"
                    is WsState.Error -> "连接异常：${state.message}"
                }
                notifyUpdate(text)
            }
        }

        // 守护循环：掉线且有 token 就重连
        scope.launch {
            while (isActive) {
                if (Request.hasToken() && !WebSocketManager.isConnected() &&
                    WebSocketManager.getConnectionState() !is WsState.Connecting
                ) {
                    WebSocketManager.connect(applicationContext)
                }
                delay(CHECK_INTERVAL_MS)
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY

    override fun onDestroy() {
        // 不主动断 WS：连接去留交回 Activity 生命周期（开关已关时 ON_STOP 会断开）
        scope.cancel()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun ensureChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID, "同步保活", NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "保持与服务器的同步连接"
                setShowBadge(false)
            }
            getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        }
    }

    private fun buildNotification(text: String): android.app.Notification {
        val contentIntent = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_sync_noanim)
            .setContentTitle("云梯同步保活")
            .setContentText(text)
            .setOngoing(true)
            .setSilent(true)
            .setContentIntent(contentIntent)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun notifyUpdate(text: String) {
        getSystemService(NotificationManager::class.java)
            .notify(FG_NOTIF_ID, buildNotification(text))
    }
}
