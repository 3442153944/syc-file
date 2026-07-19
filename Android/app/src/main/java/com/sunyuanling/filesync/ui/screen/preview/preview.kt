// ui/screen/preview/preview.kt
// 文件在线预览主界面：顶栏（文件名 + 返回 + 下载）+ 按类型分派到各渲染器。
// 加载策略见 PreviewViewModel：图片/视频/音频流式，PDF/文本/Office 下载到缓存后渲染、退出即删。
package com.sunyuanling.filesync.ui.screen.preview

import android.os.Environment
import android.widget.Toast
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Download
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.router.PreviewDestination
import com.sunyuanling.filesync.ui.viewModel.preview.PreviewUiState
import com.sunyuanling.filesync.ui.viewModel.preview.PreviewViewModel
import com.sunyuanling.filesync.ui.viewModel.transmission.DownloadListViewModel
import java.io.File

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PreviewScreen(
    args: PreviewDestination,
    onBackClick: () -> Unit,
) {
    val vm = viewModel<PreviewViewModel>()
    val downloadVm = viewModel<DownloadListViewModel>()
    val context = LocalContext.current
    val state by vm.state.collectAsState()

    LaunchedEffect(Unit) { vm.start(args) }

    fun triggerDownload() {
        val dir = File(Environment.getExternalStorageDirectory(), AppConfig.downloadDir)
        if (!dir.exists()) dir.mkdirs()
        downloadVm.addDownload(
            path = args.path,
            name = args.name,
            saveDir = dir,
            deviceId = args.deviceId.ifBlank { null }
        )
        Toast.makeText(context, "已加入下载队列", Toast.LENGTH_SHORT).show()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        text = args.name,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBackClick) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    IconButton(onClick = { triggerDownload() }) {
                        Icon(Icons.Default.Download, contentDescription = "下载")
                    }
                }
            )
        }
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
        ) {
            when (val s = state) {
                is PreviewUiState.Idle -> LoadingBox(progress = null, message = "加载中…")
                is PreviewUiState.Loading -> LoadingBox(progress = s.progress, message = s.message)
                is PreviewUiState.Image -> ImagePreview(url = s.url)
                is PreviewUiState.Media -> MediaPreview(url = s.url, isVideo = s.isVideo)
                is PreviewUiState.Pdf -> PdfPreview(file = s.file)
                is PreviewUiState.Text -> TextPreview(text = s.text, truncated = s.truncated)
                is PreviewUiState.Office -> when (val c = s.content) {
                    is com.sunyuanling.filesync.previewUtil.OfficeContent.Word -> WordPreview(c)
                    is com.sunyuanling.filesync.previewUtil.OfficeContent.Excel -> ExcelPreview(c)
                    is com.sunyuanling.filesync.previewUtil.OfficeContent.Ppt -> PptPreview(c)
                }
                is PreviewUiState.Unsupported -> MessageBox(
                    message = s.reason,
                    onDownload = { triggerDownload() }
                )
                is PreviewUiState.Error -> MessageBox(
                    message = "预览失败：${s.message}",
                    onDownload = { triggerDownload() }
                )
            }
        }
    }
}

@Composable
private fun LoadingBox(progress: Float?, message: String) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            if (progress != null) {
                LinearProgressIndicator(
                    progress = { progress },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 48.dp)
                )
                Text(
                    text = "$message ${(progress * 100).toInt()}%",
                    modifier = Modifier.padding(top = 12.dp),
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            } else {
                CircularProgressIndicator()
                Text(
                    text = message,
                    modifier = Modifier.padding(top = 12.dp),
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}

@Composable
private fun MessageBox(message: String, onDownload: () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.padding(24.dp)
        ) {
            Text(
                text = message,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            Button(
                onClick = onDownload,
                modifier = Modifier.padding(top = 16.dp)
            ) {
                Icon(Icons.Default.Download, contentDescription = null)
                Text(text = "下载文件", modifier = Modifier.padding(start = 8.dp))
            }
        }
    }
}
