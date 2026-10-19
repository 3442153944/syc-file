package com.sunyuanling.filesync.service

import android.app.*
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.drawable.GradientDrawable
import android.os.Build
import android.os.IBinder
import android.provider.Settings
import android.view.*
import android.widget.*
import com.sunyuanling.filesync.share.QuickShareActivity
import com.sunyuanling.filesync.share.QuickShareDropActivity
import com.sunyuanling.filesync.share.QuickShareController
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlin.math.abs

/** 用户开启的快传服务：可见通知、可移动投递窗；关闭窗口不丢弃正在上传的任务。 */
class QuickShareService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var window: LinearLayout? = null
    private lateinit var manager: WindowManager
    private lateinit var label: TextView
    private var progress: ProgressBar? = null
    private var details: LinearLayout? = null
    private var layout: WindowManager.LayoutParams? = null
    private var dragging = false
    private var awaitingImport = false
    private var dropSession = false
    private var startedAt = 0L
    override fun onBind(intent: Intent?): IBinder? = null
    override fun onCreate() {
        super.onCreate()
        // 队列观察器会立即收到空快照，必须先设启动时间，防止前台通知建立前误停服务。
        startedAt = android.os.SystemClock.elapsedRealtime()
        manager = getSystemService(WindowManager::class.java)
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "快传任务与悬浮窗", NotificationManager.IMPORTANCE_LOW))
        scope.launch {
            QuickShareController.tasks.collect { tasks ->
                val running = tasks.count { it.state in listOf("上传中", "等待") }
                if (::label.isInitialized) label.text = if (running > 0) "快传 · $running 个任务\n拖入文件继续分享" else "FileSync 快传\n拖入文件或点此打开"
                val active = tasks.filter { it.state in listOf("上传中", "等待") }
                val total = active.sumOf { it.size }
                progress?.progress = if (total > 0) (active.sumOf { it.sent } * 100 / total).toInt() else 0
                if (running > 0 && dropSession) awaitingImport = false
                if (running == 0 && !awaitingImport) dropSession = false
                expand(dragging || awaitingImport || (dropSession && running > 0))
                if (running == 0 && window == null && android.os.SystemClock.elapsedRealtime() - startedAt > 10_000) stopSelf()
            }
        }
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startedAt = android.os.SystemClock.elapsedRealtime()
        if (intent?.action == "stop") {
            QuickShareController.cancelAll(); hide(); stopSelf(); return START_NOT_STICKY
        }
        if (intent?.action == "hide") hide()
        val show = intent?.getBooleanExtra("overlay", false) == true && Settings.canDrawOverlays(this)
        val pending = PendingIntent.getActivity(this, 0, Intent(this, QuickShareActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(this, 1, Intent(this, QuickShareService::class.java).setAction("stop"), PendingIntent.FLAG_IMMUTABLE)
        val notification = Notification.Builder(this, CHANNEL).setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle("FileSync 快传").setContentText("点击查看队列与分享历史")
            .setContentIntent(pending).setOngoing(true).addAction(Notification.Action.Builder(null, "暂停并关闭", stop).build()).build()
        if (Build.VERSION.SDK_INT >= 34) startForeground(9031, notification,
            if (show || window != null) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        else startForeground(9031, notification)
        if (show && window == null) show()
        // 文件选择或暂存取消时也会退出空服务，活动任务则由队列观察器负责收尾。
        scope.launch { delay(15_000); if (window == null && QuickShareController.tasks.value.none { it.state in listOf("等待", "上传中") }) stopSelf() }
        return START_NOT_STICKY
    }
    private fun show() {
        val density = resources.displayMetrics.density
        val params = WindowManager.LayoutParams((52 * density).toInt(), WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
            PixelFormat.TRANSLUCENT).apply { gravity = Gravity.TOP or Gravity.START; x = (16 * density).toInt(); y = (180 * density).toInt() }
        val panel = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; setPadding(4, 4, 4, 4)
            background = GradientDrawable().apply { setColor(Color.argb(80, 30, 51, 80)); cornerRadius = 32 * density }
            alpha = 0.65f
            elevation = 12f
        }
        val icon = ImageView(this).apply {
            setImageDrawable(applicationInfo.loadIcon(packageManager))
            contentDescription = "FileSync 快传悬浮图标"
            setPadding(4, 4, 4, 4)
        }
        label = TextView(this).apply { text = "FileSync 快传\n拖入文件或点此打开"; textSize = 14f; setTextColor(Color.WHITE); setPadding(8, 12, 8, 12) }
        var startX = 0f; var startY = 0f; var originX = 0; var originY = 0; var moved = false
        icon.setOnTouchListener { _, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> { startX = event.rawX; startY = event.rawY; originX = params.x; originY = params.y; moved = false }
                MotionEvent.ACTION_MOVE -> {
                    moved = moved || abs(event.rawX - startX) + abs(event.rawY - startY) > 12
                    params.x = (originX + event.rawX - startX).toInt().coerceIn(0, (resources.displayMetrics.widthPixels - params.width).coerceAtLeast(0))
                    params.y = (originY + event.rawY - startY).toInt().coerceIn(0, (resources.displayMetrics.heightPixels - panel.height).coerceAtLeast(0))
                    manager.updateViewLayout(panel, params)
                }
                MotionEvent.ACTION_UP -> if (!moved) startActivity(Intent(this, QuickShareActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
            true
        }
        panel.addView(icon, LinearLayout.LayoutParams((48 * density).toInt(), (48 * density).toInt()))
        details = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; visibility = View.GONE
            addView(label)
            progress = ProgressBar(this@QuickShareService, null, android.R.attr.progressBarStyleHorizontal).apply { max = 100 }
            addView(progress, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, (8 * density).toInt()))
            addView(Button(this@QuickShareService).apply { text = "查看队列与分享"; setOnClickListener {
                startActivity(Intent(this@QuickShareService, QuickShareActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } })
            addView(Button(this@QuickShareService).apply { text = "关闭悬浮窗"; setOnClickListener { hide() } })
        }
        panel.addView(details)
        panel.setOnDragListener { _, event ->
            when (event.action) {
                DragEvent.ACTION_DRAG_STARTED -> true
                DragEvent.ACTION_DRAG_ENTERED -> { dragging = true; expand(true); label.text = "松开立即快传"; true }
                DragEvent.ACTION_DROP -> {
                    if (event.clipData == null) false else {
                        awaitingImport = true
                        dropSession = true
                        // 由 Activity 接收系统 DragAndDropPermissions，暂存完成前保持授权。
                        startActivity(Intent(this, QuickShareDropActivity::class.java)
                            .putExtra("drop", event).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                        scope.launch { delay(10_000); awaitingImport = false
                            expand(dropSession && QuickShareController.tasks.value.any { it.state in listOf("上传中", "等待") }) }
                        true
                    }
                }
                DragEvent.ACTION_DRAG_ENDED, DragEvent.ACTION_DRAG_EXITED -> {
                    dragging = false
                    expand(awaitingImport || (dropSession && QuickShareController.tasks.value.any { it.state in listOf("上传中", "等待") }))
                    true
                }
                else -> true
            }
        }
        manager.addView(panel, params); window = panel; layout = params; overlayState.value = true
    }
    private fun expand(value: Boolean) {
        val panel = window ?: return
        val params = layout ?: return
        val width = ((if (value) 220 else 52) * resources.displayMetrics.density).toInt()
        details?.visibility = if (value) View.VISIBLE else View.GONE
        panel.alpha = if (value) 0.96f else 0.65f
        (panel.background as? GradientDrawable)?.setColor(if (value) Color.rgb(30, 51, 80) else Color.argb(80, 30, 51, 80))
        if (params.width != width) { params.width = width
            params.x = params.x.coerceAtMost((resources.displayMetrics.widthPixels - width).coerceAtLeast(0))
            manager.updateViewLayout(panel, params) }
    }
    private fun hide() { window?.let { manager.removeView(it) }; window = null; layout = null; details = null; overlayState.value = false }
    override fun onDestroy() { hide(); scope.cancel(); super.onDestroy() }
    override fun onTimeout(startId: Int, fgsType: Int) { QuickShareController.cancelAll(); stopSelf() }
    companion object {
        private const val CHANNEL = "quick_share"
        private val overlayState = MutableStateFlow(false)
        val overlay = overlayState.asStateFlow()
        fun start(context: Context, overlay: Boolean = false) {
            context.startForegroundService(Intent(context, QuickShareService::class.java).putExtra("overlay", overlay))
        }
        fun hideOverlay(context: Context) { context.startService(Intent(context, QuickShareService::class.java).setAction("hide")) }
    }
}
