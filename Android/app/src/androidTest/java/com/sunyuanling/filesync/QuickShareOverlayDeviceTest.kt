package com.sunyuanling.filesync

import android.content.*
import android.os.SystemClock
import android.provider.Settings
import android.view.InputDevice
import android.view.MotionEvent
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.service.QuickShareService
import com.sunyuanling.filesync.share.*
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class QuickShareOverlayDeviceTest {
    @Test fun crossAppDragUploadsAndCopiesVerifiedLink() = runBlocking {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val context = instrumentation.targetContext
        assertTrue("开发实例需开启待测应用悬浮窗权限", Settings.canDrawOverlays(context))
        val oldCopy = QuickShareController.autoCopy
        val oldIds = QuickShareController.tasks.value.map { it.id }.toSet()
        var created: ShareTask? = null
        try {
            QuickShareController.autoCopy = true
            context.startActivity(Intent(context, QuickShareActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            delay(1500)
            QuickShareService.start(context, true)
            withTimeout(5000) { while (!QuickShareService.overlay.value) delay(50) }
            context.startActivity(Intent().setComponent(ComponentName("com.sunyuanling.filesync.test", "com.sunyuanling.filesync.DragSourceActivity"))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            delay(1500)
            val down = SystemClock.uptimeMillis()
            fun event(action: Int, x: Float, y: Float) {
                val event = MotionEvent.obtain(down, SystemClock.uptimeMillis(), action, x, y, 0)
                event.source = InputDevice.SOURCE_TOUCHSCREEN
                check(instrumentation.uiAutomation.injectInputEvent(event, true)); event.recycle()
            }
            event(MotionEvent.ACTION_DOWN, 450f, 1100f)
            delay(800)
            repeat(30) { step -> event(MotionEvent.ACTION_MOVE, 450f - 330f * (step + 1) / 30, 1100f - 680f * (step + 1) / 30); delay(30) }
            event(MotionEvent.ACTION_UP, 120f, 420f)
            created = withTimeout(20_000) {
                while (true) {
                    val item = QuickShareController.tasks.value.firstOrNull { it.id !in oldIds && it.name == "drag-source.txt" }
                    if (item != null) return@withTimeout item
                    delay(100)
                }
                error("未接收到拖放文件")
            }
            val id = created.id
            val finished = withTimeout(90_000) {
                while (true) {
                    val item = QuickShareController.tasks.value.first { it.id == id }
                    check(item.state != "失败") { item.error }
                    if (item.result != null) return@withTimeout item
                    delay(100)
                }
                error("未完成")
            }
            created = finished
            // 回到本应用前台再读取剪贴板，避免 Android 的后台读取限制影响断言。
            context.startActivity(Intent(context, QuickShareActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            delay(1500)
            var copied = ""
            instrumentation.runOnMainSync {
                copied = context.getSystemService(ClipboardManager::class.java).primaryClip?.getItemAt(0)?.text?.toString().orEmpty()
            }
            assertEquals(finished.result!!.url(finished.base), copied)
        } finally {
            QuickShareController.autoCopy = oldCopy
            QuickShareService.hideOverlay(context)
            created?.let { task ->
                task.result?.let { link -> ShareTransport(task.base, Request.getToken() ?: error("未登录"), Request.deviceId())
                    .post("/file/share-link/revoke", buildJsonObject { put("share_code", link.share_code) }) }
                QuickShareController.remove(task.id)
            }
        }
    }
}
