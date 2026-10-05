// ui/components/preview/OpenWithButton.kt
// 通用组件：「用其他应用打开」按钮。
// 文件从哪来由调用方通过 [resolveFile] 决定（本地已有就直接返回，否则先下载再返回），
// 组件负责：点击后显示进度、调用 ExternalOpener 拉起系统选择器、失败时提示原因。
// 图片预览用它，之后 PDF / 文档 / 下载完成后的「打开」都可以直接复用。
package com.sunyuanling.filesync.ui.components.preview

import android.widget.Toast
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.sunyuanling.filesync.util.ExternalOpener
import kotlinx.coroutines.launch
import java.io.File

/**
 * @param resolveFile 取得要打开的本地文件；失败返回带可读原因的 Result.failure（会原样提示给用户）
 * @param mimeType 指定 MIME；为空按文件扩展名推断
 */
@Composable
fun OpenWithButton(
    resolveFile: suspend () -> Result<File>,
    modifier: Modifier = Modifier,
    mimeType: String? = null,
    enabled: Boolean = true,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }

    IconButton(
        onClick = {
            if (busy) return@IconButton
            busy = true
            scope.launch {
                val result = resolveFile().mapCatching { file ->
                    ExternalOpener.open(context, file, mimeType).getOrThrow()
                }
                busy = false
                result.onFailure {
                    Toast.makeText(context, it.message ?: "无法打开文件", Toast.LENGTH_SHORT).show()
                }
            }
        },
        enabled = enabled && !busy,
        modifier = modifier
    ) {
        if (busy) {
            CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
        } else {
            Icon(Icons.AutoMirrored.Filled.OpenInNew, contentDescription = "用其他应用打开")
        }
    }
}
