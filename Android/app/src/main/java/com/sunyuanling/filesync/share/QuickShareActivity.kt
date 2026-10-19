package com.sunyuanling.filesync.share

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.DragEvent
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.lifecycle.lifecycleScope
import com.sunyuanling.filesync.service.QuickShareService
import com.sunyuanling.filesync.ui.screen.files.QuickShareScreen
import com.sunyuanling.filesync.ui.theme.FileSyncTheme
import kotlinx.coroutines.launch

/** 系统分享入口与应用内管理页共用界面，不要求悬浮窗权限。 */
open class QuickShareActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { FileSyncTheme { QuickShareScreen { finish() } } }
        if (savedInstanceState == null) receive(intent)
    }
    protected open fun receive(intent: Intent) {
        val uris = when (intent.action) {
            Intent.ACTION_SEND -> listOfNotNull(intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM))
            Intent.ACTION_SEND_MULTIPLE -> intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM).orEmpty()
            else -> emptyList()
        }
        if (uris.isNotEmpty()) import(uris)
    }
    protected fun import(uris: List<Uri>, after: () -> Unit = {}) {
        lifecycleScope.launch {
            try { QuickShareService.start(this@QuickShareActivity); QuickShareController.importUris(uris) }
            catch (e: Exception) { Toast.makeText(this@QuickShareActivity, e.message ?: "无法接收文件", Toast.LENGTH_LONG).show() }
            finally { after() }
        }
    }
}

/** 仅供本应用悬浮窗调用，外部应用不能伪造拖放事件。 */
class QuickShareDropActivity : QuickShareActivity() {
    override fun receive(intent: Intent) {
        val event = intent.getParcelableExtra<DragEvent>("drop") ?: return finish()
        val permissions = requestDragAndDropPermissions(event)
        val clip = event.clipData ?: return finish()
        val uris = (0 until clip.itemCount).mapNotNull { clip.getItemAt(it).uri }
        if (uris.isEmpty()) { permissions?.release(); finish(); return }
        import(uris) { permissions?.release(); finish() }
    }
}
