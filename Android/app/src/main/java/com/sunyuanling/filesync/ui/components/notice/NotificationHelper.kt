package com.sunyuanling.filesync.ui.components.notice

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.annotation.RequiresPermission
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.sunyuanling.filesync.MainActivity
import com.sunyuanling.filesync.R
import com.sunyuanling.filesync.util.formatFileSize
import com.sunyuanling.filesync.util.formatSpeed

object DownloadNotificationHelper {

    private const val CHANNEL_ID_PROGRESS = "download_progress"
    private const val CHANNEL_ID_COMPLETE = "download_complete"

    fun createChannels(context: Context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val manager = context.getSystemService(NotificationManager::class.java)

            // 进度通知频道：静音
            NotificationChannel(
                CHANNEL_ID_PROGRESS,
                "下载进度",
                NotificationManager.IMPORTANCE_LOW  // 静音，不打扰
            ).also { manager.createNotificationChannel(it) }

            // 完成通知频道：有提示音
            NotificationChannel(
                CHANNEL_ID_COMPLETE,
                "下载完成",
                NotificationManager.IMPORTANCE_DEFAULT  // 默认声音
            ).apply {
                description = "文件下载完成时通知"
                enableVibration(true)
            }.also { manager.createNotificationChannel(it) }
        }
    }

    // 显示/更新进度通知
    @RequiresPermission(Manifest.permission.POST_NOTIFICATIONS)
    fun showProgress(
        context: Context,
        notificationId: Int,
        fileName: String,
        progress: Int,       // 0-100
        downloadedBytes: Long,
        totalBytes: Long,
        speed: Long          // bytes/s
    ) {
        val notification = NotificationCompat.Builder(context, CHANNEL_ID_PROGRESS)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentTitle(fileName)
            .setContentText("${formatFileSize(downloadedBytes)} / ${formatFileSize(totalBytes)}  ${formatSpeed(speed)}")
            .setProgress(100, progress, totalBytes <= 0)  // totalBytes<=0 时显示不确定进度条
            .setOngoing(true)       // 不可手动划掉
            .setOnlyAlertOnce(true) // 只在第一次出现时响
            .setSilent(true)        // 进度更新静音
            .build()

        NotificationManagerCompat.from(context)
            .notify(notificationId, notification)
    }

    // 下载完成通知（有提示音）
    @RequiresPermission(Manifest.permission.POST_NOTIFICATIONS)
    fun showComplete(
        context: Context,
        notificationId: Int,
        fileName: String,
        fileSize: Long,
        filePath: String
    ) {
        // 点击通知打开文件所在目录（或直接打开文件）
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
            .setAutoCancel(true)    // 点击后自动消失
            .build()

        NotificationManagerCompat.from(context)
            .notify(notificationId, notification)
    }

    // 下载失败通知
    @RequiresPermission(Manifest.permission.POST_NOTIFICATIONS)
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

        NotificationManagerCompat.from(context)
            .notify(notificationId, notification)
    }

    // 取消通知（下载取消时移除进度条）
    fun cancel(context: Context, notificationId: Int) {
        NotificationManagerCompat.from(context).cancel(notificationId)
    }
}