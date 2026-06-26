package com.sunyuanling.filesync.service

import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.ServiceCompat
import com.sunyuanling.filesync.dataClass.DownloadStatus
import com.sunyuanling.filesync.ui.components.notice.DownloadNotificationHelper
import com.sunyuanling.filesync.ui.viewModel.data.DownloadController
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

/**
 * 前台下载服务。承载 PRDownloader 下载运行期间的前台通知（含通知栏操作），
 * 在有活跃下载时保持前台；无活跃下载时自停。
 *
 * 被系统杀死后：START_STICKY 可能重启本服务，但此时 Controller 无活跃任务，
 * 会立即自停；被杀任务的自动续传受 PRDownloader 进程内 id 限制，需用户重试。
 */
class DownloadService : Service() {

    companion object {
        const val FG_NOTIF_ID = 9001
        const val EXTRA_ACTION = "action"
        const val EXTRA_DOWNLOAD_ID = "download_id"
        const val ACTION_PAUSE = "pause"
        const val ACTION_RESUME = "resume"
        const val ACTION_CANCEL = "cancel"

        fun commandIntent(ctx: Context, action: String, downloadId: String): Intent =
            Intent(ctx, DownloadService::class.java)
                .putExtra(EXTRA_ACTION, action)
                .putExtra(EXTRA_DOWNLOAD_ID, downloadId)
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    override fun onCreate() {
        super.onCreate()
        val base = DownloadNotificationHelper.buildForegroundBase(this)
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
        ServiceCompat.startForeground(this, FG_NOTIF_ID, base, type)

        scope.launch {
            DownloadController.downloads.collect { list ->
                val active = list.filter {
                    it.status == DownloadStatus.Downloading ||
                            it.status == DownloadStatus.Paused ||
                            it.status == DownloadStatus.Waiting
                }
                if (active.isEmpty()) {
                    stopForegroundAndSelf()
                } else {
                    DownloadNotificationHelper.notifyForeground(this@DownloadService, FG_NOTIF_ID, active)
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.getStringExtra(EXTRA_ACTION)) {
            ACTION_PAUSE -> intent.getStringExtra(EXTRA_DOWNLOAD_ID)?.let {
                DownloadController.pauseDownload(it)
            }
            ACTION_RESUME -> intent.getStringExtra(EXTRA_DOWNLOAD_ID)?.let {
                DownloadController.resumeDownload(it)
            }
            ACTION_CANCEL -> intent.getStringExtra(EXTRA_DOWNLOAD_ID)?.let {
                DownloadController.cancelDownload(it)
            }
        }
        return START_STICKY
    }

    private fun stopForegroundAndSelf() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE)
        } else {
            @Suppress("DEPRECATION")
            stopForeground(true)
        }
        stopSelf()
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null
}
