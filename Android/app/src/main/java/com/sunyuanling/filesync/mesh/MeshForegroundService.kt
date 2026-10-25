package com.sunyuanling.filesync.mesh

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
import com.sunyuanling.filesync.MainActivity
import kotlinx.coroutines.*

/** User-started mesh connection, with visible state and an explicit disconnect action. */
class MeshForegroundService : Service() {
    companion object {
        const val NOTIFICATION_ID = 9003
        const val CHANNEL_ID = "mesh_network"
        private const val DISCONNECT = "mesh.disconnect"
        fun start(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, MeshForegroundService::class.java))
        }
    }
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    override fun onCreate() {
        super.onCreate()
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(CHANNEL_ID, "P2P 组网状态", NotificationManager.IMPORTANCE_LOW).apply {
            description = "当前网络、直连/中继、握手和流量"
            setShowBadge(false)
        })
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, NOTIFICATION_ID, notification(MeshController.state.value), type)
        scope.launch {
            MeshController.state.collect { state ->
                if (!state.running && !state.busy) {
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf()
                } else manager.notify(NOTIFICATION_ID, notification(state))
            }
        }
    }

    private fun notification(state: MeshController.State): android.app.Notification {
        val open = PendingIntent.getActivity(this, 9003, Intent(this, MainActivity::class.java), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val disconnect = PendingIntent.getService(this, 9003,
            Intent(this, MeshForegroundService::class.java).setAction(DISCONNECT), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val direct = state.peers.count { it.mode == "lan" || it.mode == "direct" }
        val relay = state.peers.count { it.mode == "relay" }
        val summary = if (state.busy) state.message else "直连 $direct · 中继 $relay"
        val details = buildString {
            append("${state.network}\n${state.backend} · ${state.address}\n${state.message}")
            append("\n接收 ${state.received} B · 发送 ${state.sent} B")
            state.peers.forEach { append("\n${it.address} · ${it.label} · ${it.endpoint}") }
        }
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_sync_noanim)
            .setContentTitle("P2P 组网 · $summary")
            .setContentText("${state.network} · ${state.address}")
            .setStyle(NotificationCompat.BigTextStyle().bigText(details))
            .setContentIntent(open).setOngoing(true).setOnlyAlertOnce(true).setSilent(true)
            .addAction(0, "断开组网", disconnect).build()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == DISCONNECT) MeshController.requestDisconnect()
        return START_NOT_STICKY
    }
    override fun onDestroy() {
        scope.cancel()
        if (MeshController.state.value.running || MeshController.state.value.busy) MeshController.requestDisconnect()
        super.onDestroy()
    }
    override fun onBind(intent: Intent?): IBinder? = null
}
