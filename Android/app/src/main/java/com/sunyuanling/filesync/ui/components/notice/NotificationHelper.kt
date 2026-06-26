package com.sunyuanling.filesync.ui.components.notice

import android.Manifest
import android.annotation.SuppressLint
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.sunyuanling.filesync.MainActivity
import com.sunyuanling.filesync.dataClass.DownloadItem
import com.sunyuanling.filesync.dataClass.DownloadStatus
import com.sunyuanling.filesync.service.DownloadService
import com.sunyuanling.filesync.util.formatFileSize
import com.sunyuanling.filesync.util.formatSpeed

object DownloadNotificationHelper {

    private const val CHANNEL_ID_PROGRESS = "download_progress"
    private const val CHANNEL_ID_COMPLETE = "download_complete"

    fun Context.hasNotificationPermission(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            ContextCompat.checkSelfPermission(
                this, Manifest.permission.POST_NOTIFICATIONS
            ) == PackageManager.PERMISSION_GRANTED
        } else {
            true
        }
    }

    fun createChannels(context: Context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val manager = context.getSystemService(NotificationManager::class.java)
            NotificationChannel(
                CHANNEL_ID_PROGRESS,
                "下载进度",
                NotificationManager.IMPORTANCE_LOW
            ).also { manager.createNotificationChannel(it) }
            NotificationChannel(
                CHANNEL_ID_COMPLETE,
                "下载完成",
                NotificationManager.IMPORTANCE_DEFAULT
            ).apply {
                description = "文件下载完成时通知"
                enableVibration(true)
            }.also { manager.createNotificationChannel(it) }
        }
    }

    /** 前台服务启动时立即使用的基线通知（避免 5s 未 startForeground 崩溃） */
    fun buildForegroundBase(context: Context) =
        NotificationCompat.Builder(context, CHANNEL_ID_PROGRESS)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentTitle("云梯下载")
            .setContentText("下载服务运行中")
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .build()

    /** 根据活跃下载列表更新前台通知（含通知栏操作按钮） */
    @SuppressLint("MissingPermission")
    fun notifyForeground(context: Context, notificationId: Int, active: List<DownloadItem>) {
        if (!context.hasNotificationPermission()) return

        val builder = NotificationCompat.Builder(context, CHANNEL_ID_PROGRESS)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)

        if (active.size == 1) {
            val item = active[0]
            val speedText = if (item.status == DownloadStatus.Downloading && item.speed > 0)
                "  ${formatSpeed(item.speed)}" else ""
            builder.setContentTitle(item.fileName)
                .setContentText("${formatFileSize(item.downloadedSize)} / ${formatFileSize(item.totalSize)}$speedText")
                .setProgress(100, item.progress, item.totalSize <= 0)

            when (item.status) {
                DownloadStatus.Downloading -> {
                    builder.addAction(0, "暂停", actionPI(context, DownloadService.ACTION_PAUSE, item.downloadId))
                    builder.addAction(0, "取消", actionPI(context, DownloadService.ACTION_CANCEL, item.downloadId))
                }
                DownloadStatus.Paused -> {
                    builder.addAction(0, "恢复", actionPI(context, DownloadService.ACTION_RESUME, item.downloadId))
                    builder.addAction(0, "取消", actionPI(context, DownloadService.ACTION_CANCEL, item.downloadId))
                }
                DownloadStatus.Waiting -> {
                    builder.addAction(0, "取消", actionPI(context, DownloadService.ACTION_CANCEL, item.downloadId))
                }
                else -> {}
            }
        } else {
            builder.setContentTitle("正在下载 ${active.size} 个文件")
                .setContentText("点查看传输列表")
                .setProgress(0, 0, true)
            val open = Intent(context, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_SINGLE_TOP
            }
            builder.setContentIntent(
                PendingIntent.getActivity(
                    context, 0, open,
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
                )
            )
        }
        NotificationManagerCompat.from(context).notify(notificationId, builder.build())
    }

    private fun actionPI(context: Context, action: String, downloadId: String): PendingIntent {
        val intent = DownloadService.commandIntent(context, action, downloadId)
        val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M)
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        else PendingIntent.FLAG_UPDATE_CURRENT
        return PendingIntent.getService(
            context, downloadId.hashCode() xor action.hashCode(), intent, flags
        )
    }

    // 显示/更新进度通知（保留兼容；前台通知由 notifyForeground 负责）
    @SuppressLint("MissingPermission")
    fun showProgress(
        context: Context,
        notificationId: Int,
        fileName: String,
        progress: Int,
        downloadedBytes: Long,
        totalBytes: Long,
        speed: Long
    ) {
        if (!context.hasNotificationPermission()) return
        val notification = NotificationCompat.Builder(context, CHANNEL_ID_PROGRESS)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentTitle(fileName)
            .setContentText("${formatFileSize(downloadedBytes)} / ${formatFileSize(totalBytes)}  ${formatSpeed(speed)}")
            .setProgress(100, progress, totalBytes <= 0)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .build()
        NotificationManagerCompat.from(context).notify(notificationId, notification)
    }

    @SuppressLint("MissingPermission")
    fun showComplete(
        context: Context,
        notificationId: Int,
        fileName: String,
        fileSize: Long,
        filePath: String
    ) {
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP
            putExtra("open_file", filePath)
        }
        val pendingIntent = PendingIntent.getActivity(
            context, notificationId, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val notification = NotificationCompat.Builder(context, CHANNEL_ID_COMPLETE)
            .setSmallIcon(android.R.drawable.stat_sys_download_done)
            .setContentTitle("下载完成")
            .setContentText("$fileName  ${formatFileSize(fileSize)}")
            .setContentIntent(pendingIntent)
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(context).notify(notificationId, notification)
    }

    @SuppressLint("MissingPermission")
    fun showFailed(
        context: Context,
        notificationId: Int,
        fileName: String,
        reason: String
    ) {
        val notification = NotificationCompat.Builder(context, CHANNEL_ID_COMPLETE)
            .setSmallIcon(android.R.drawable.stat_notify_error)
            .setContentTitle("下载失败")
            .setContentText("$fileName：$reason")
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(context).notify(notificationId, notification)
    }

    fun cancel(context: Context, notificationId: Int) {
        NotificationManagerCompat.from(context).cancel(notificationId)
    }
}
