// ui/components/update/UpdateDialog.kt
// 更新提示框：由 UpdateController.available 驱动。强制更新（mandatory）时不可关闭。
package com.sunyuanling.filesync.ui.components.update

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sunyuanling.filesync.update.UpdateController
import com.sunyuanling.filesync.util.formatFileSize

@Composable
fun UpdateDialog() {
    val available by UpdateController.available.collectAsState()
    val dl by UpdateController.downloadState.collectAsState()
    val context = LocalContext.current
    val info = available ?: return

    val downloading = dl is UpdateController.DownloadState.Downloading ||
            dl is UpdateController.DownloadState.Verifying
    val isError = dl is UpdateController.DownloadState.Error

    AlertDialog(
        onDismissRequest = { if (!info.mandatory && !downloading) UpdateController.dismiss() },
        title = {
            Text(if (info.mandatory) "发现重要更新" else "发现新版本")
        },
        text = {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(6.dp)
            ) {
                Text("版本：${info.release.versionName}", fontSize = 14.sp)
                if (info.release.fileSize > 0) {
                    Text("大小：${formatFileSize(info.release.fileSize)}", fontSize = 13.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (info.release.notes.isNotBlank()) {
                    Text(info.release.notes, fontSize = 13.sp)
                }
                if (info.mandatory) {
                    Text("此为强制更新，需完成后才能继续使用。", fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.error)
                }
                when (val s = dl) {
                    is UpdateController.DownloadState.Downloading -> {
                        val p = s.progress
                        if (p != null) {
                            LinearProgressIndicator(progress = { p }, modifier = Modifier.fillMaxWidth())
                            Text("下载中 ${(p * 100).toInt()}%", fontSize = 12.sp)
                        } else {
                            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                            Text("下载中…", fontSize = 12.sp)
                        }
                    }
                    is UpdateController.DownloadState.Verifying ->
                        Text("校验安装包…", fontSize = 12.sp)
                    is UpdateController.DownloadState.ReadyToInstall ->
                        Text("准备安装…", fontSize = 12.sp)
                    is UpdateController.DownloadState.Error ->
                        Text(s.message, fontSize = 12.sp, color = MaterialTheme.colorScheme.error)
                    else -> {}
                }
            }
        },
        confirmButton = {
            Button(
                onClick = { UpdateController.downloadAndInstall(context) },
                enabled = !downloading
            ) {
                Text(if (isError) "重试" else "下载并安装")
            }
        },
        dismissButton = {
            if (!info.mandatory && !downloading) {
                TextButton(onClick = { UpdateController.dismiss() }) { Text("稍后") }
            }
        }
    )
}
