// ui/screen/preview/PreviewRenderers.kt
// 职责：各预览类型的具体渲染 Composable。
//  - ImagePreview：Coil 加载 + 双指缩放/拖动
//  - MediaPreview：Media3 ExoPlayer 流式播放（视频/音频，支持 Range 拖动）
//  - PdfPreview：系统 PdfRenderer 按页懒渲染为位图
//  - TextPreview：等宽可滚动文本
//  - WordPreview / ExcelPreview / PptPreview：POI 解析结果渲染
package com.sunyuanling.filesync.ui.screen.preview

import android.graphics.Bitmap
import android.graphics.pdf.PdfRenderer
import android.os.ParcelFileDescriptor
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import coil.compose.AsyncImage
import coil.request.ImageRequest
import com.sunyuanling.filesync.previewUtil.ExcelSheet
import com.sunyuanling.filesync.previewUtil.OfficeContent
import com.sunyuanling.filesync.previewUtil.PptSlide
import com.sunyuanling.filesync.previewUtil.WordBlock
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File

// ==================== 图片 ====================

@Composable
fun ImagePreview(url: String, modifier: Modifier = Modifier) {
    var scale by remember { mutableStateOf(1f) }
    var offsetX by remember { mutableStateOf(0f) }
    var offsetY by remember { mutableStateOf(0f) }
    val context = LocalContext.current

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(androidx.compose.ui.graphics.Color.Black)
            .pointerInput(Unit) {
                detectTransformGestures { _, pan, zoom, _ ->
                    scale = (scale * zoom).coerceIn(1f, 6f)
                    if (scale > 1f) {
                        offsetX += pan.x
                        offsetY += pan.y
                    } else {
                        offsetX = 0f; offsetY = 0f
                    }
                }
            },
        contentAlignment = Alignment.Center
    ) {
        AsyncImage(
            model = ImageRequest.Builder(context).data(url).crossfade(true).build(),
            contentDescription = "图片预览",
            contentScale = ContentScale.Fit,
            modifier = Modifier
                .fillMaxSize()
                .graphicsLayer(
                    scaleX = scale,
                    scaleY = scale,
                    translationX = offsetX,
                    translationY = offsetY
                )
        )
    }
}

// ==================== 视频 / 音频（Media3 ExoPlayer 流式） ====================

@androidx.annotation.OptIn(UnstableApi::class)
@Composable
fun MediaPreview(url: String, isVideo: Boolean, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val exoPlayer = remember(url) {
        ExoPlayer.Builder(context).build().apply {
            setMediaItem(MediaItem.fromUri(url))
            prepare()
            playWhenReady = false
        }
    }

    DisposableEffect(url) {
        onDispose { exoPlayer.release() }
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(androidx.compose.ui.graphics.Color.Black),
        contentAlignment = Alignment.Center
    ) {
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    player = exoPlayer
                    useController = true
                    if (!isVideo) {
                        // 音频无画面：显示默认封面，控制条常驻
                        setShowBuffering(PlayerView.SHOW_BUFFERING_ALWAYS)
                        controllerShowTimeoutMs = 0
                        controllerHideOnTouch = false
                    }
                }
            },
            modifier = if (isVideo) Modifier.fillMaxSize() else Modifier.fillMaxWidth()
        )
        if (!isVideo) {
            Text(
                text = "♪",
                fontSize = 96.sp,
                color = androidx.compose.ui.graphics.Color(0xFF666666),
                modifier = Modifier.padding(bottom = 120.dp)
            )
        }
    }
}

// ==================== PDF（系统 PdfRenderer 逐页懒渲染） ====================

@Composable
fun PdfPreview(file: File, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    // renderer 与 fd 生命周期绑定到该文件；PdfRenderer 非线程安全 → 用 mutex 串行化 render
    val holder = remember(file) { PdfRendererHolder(file) }
    val renderMutex = remember(file) { Mutex() }

    DisposableEffect(file) {
        onDispose { holder.close() }
    }

    val renderer = holder.renderer
    if (renderer == null) {
        Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text("无法打开 PDF", color = MaterialTheme.colorScheme.error)
        }
        return
    }

    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .background(androidx.compose.ui.graphics.Color(0xFF303030))
            .padding(8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        items(holder.pageCount) { index ->
            PdfPage(renderer = renderer, mutex = renderMutex, index = index)
        }
    }
}

@Composable
private fun PdfPage(renderer: PdfRenderer, mutex: Mutex, index: Int) {
    // 每页目标宽度（px），控制位图内存
    val targetWidth = 1080
    val bitmapState = produceState<Bitmap?>(initialValue = null, key1 = index) {
        value = withContext(Dispatchers.IO) {
            mutex.withLock {
                runCatching {
                    renderer.openPage(index).use { page ->
                        val scale = targetWidth.toFloat() / page.width
                        val w = targetWidth
                        val h = (page.height * scale).toInt().coerceAtLeast(1)
                        val bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
                        bmp.eraseColor(android.graphics.Color.WHITE)
                        page.render(bmp, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                        bmp
                    }
                }.getOrNull()
            }
        }
    }

    val bmp = bitmapState.value
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (bmp == null) Modifier.aspectRatio(0.7f) else Modifier)
            .background(androidx.compose.ui.graphics.Color.White),
        contentAlignment = Alignment.Center
    ) {
        if (bmp == null) {
            CircularProgressIndicator()
        } else {
            Image(
                bitmap = bmp.asImageBitmap(),
                contentDescription = "第 ${index + 1} 页",
                modifier = Modifier.fillMaxWidth(),
                contentScale = ContentScale.FillWidth
            )
        }
    }
}

/** 持有 PdfRenderer 与其 ParcelFileDescriptor，统一关闭。 */
private class PdfRendererHolder(file: File) {
    var renderer: PdfRenderer? = null
        private set
    var pageCount: Int = 0
        private set
    private var pfd: ParcelFileDescriptor? = null

    init {
        runCatching {
            val descriptor = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
            pfd = descriptor
            renderer = PdfRenderer(descriptor).also { pageCount = it.pageCount }
        }
    }

    fun close() {
        runCatching { renderer?.close() }
        runCatching { pfd?.close() }
        renderer = null
    }
}

// ==================== 纯文本 ====================

@Composable
fun TextPreview(text: String, truncated: Boolean, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surface)
            .verticalScroll(rememberScrollState())
            .padding(12.dp)
    ) {
        if (truncated) {
            Text(
                text = "内容较大，仅显示前约 1MB",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(bottom = 8.dp)
            )
        }
        SelectionContainer {
            Text(
                text = text,
                fontFamily = FontFamily.Monospace,
                fontSize = 13.sp,
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
            )
        }
    }
}

// ==================== Office：Word ====================

@Composable
fun WordPreview(content: OfficeContent.Word, modifier: Modifier = Modifier) {
    if (content.blocks.isEmpty()) {
        EmptyDoc(modifier, "文档为空或无可提取文本")
        return
    }
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surface)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        items(content.blocks) { block ->
            when (block) {
                is WordBlock.Para -> Text(text = block.text, fontSize = 15.sp)
                is WordBlock.Table -> SimpleTable(rows = block.rows)
            }
        }
    }
}

// ==================== Office：Excel ====================

@Composable
fun ExcelPreview(content: OfficeContent.Excel, modifier: Modifier = Modifier) {
    if (content.sheets.isEmpty()) {
        EmptyDoc(modifier, "工作簿为空")
        return
    }
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surface)
            .padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        items(content.sheets) { sheet: ExcelSheet ->
            Column {
                Text(
                    text = "工作表：${sheet.name}",
                    fontWeight = FontWeight.Bold,
                    fontSize = 15.sp,
                    modifier = Modifier.padding(bottom = 6.dp)
                )
                if (sheet.rows.isEmpty()) {
                    Text("（空表）", color = MaterialTheme.colorScheme.outline, fontSize = 13.sp)
                } else {
                    SimpleTable(rows = sheet.rows)
                }
                if (sheet.truncated) {
                    Text(
                        text = "内容较多，仅显示部分行/列",
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(top = 4.dp)
                    )
                }
            }
        }
    }
}

// ==================== Office：PPT ====================

@Composable
fun PptPreview(content: OfficeContent.Ppt, modifier: Modifier = Modifier) {
    if (content.slides.isEmpty()) {
        EmptyDoc(modifier, "演示文稿为空")
        return
    }
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surface)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        items(content.slides) { slide: PptSlide ->
            Card(modifier = Modifier.fillMaxWidth()) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = "第 ${slide.index} 页",
                        fontWeight = FontWeight.Bold,
                        fontSize = 14.sp,
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(bottom = 8.dp)
                    )
                    if (slide.lines.isEmpty()) {
                        Text("（本页无文本）", color = MaterialTheme.colorScheme.outline, fontSize = 13.sp)
                    } else {
                        slide.lines.forEach { line ->
                            Text(text = line, fontSize = 15.sp, modifier = Modifier.padding(vertical = 2.dp))
                        }
                    }
                }
            }
        }
    }
}

// ==================== 公共小组件 ====================

/** 简易表格：横向可滚动，边框分隔。首行加粗当表头。 */
@Composable
private fun SimpleTable(rows: List<List<String>>) {
    val colCount = rows.maxOfOrNull { it.size } ?: 0
    if (colCount == 0) return
    val cellWidth = 120.dp
    Column(
        modifier = Modifier
            .horizontalScroll(rememberScrollState())
            .border(1.dp, MaterialTheme.colorScheme.outlineVariant)
    ) {
        rows.forEachIndexed { rowIndex, row ->
            Row {
                for (c in 0 until colCount) {
                    val value = row.getOrNull(c).orEmpty()
                    Text(
                        text = value,
                        fontSize = 13.sp,
                        fontWeight = if (rowIndex == 0) FontWeight.Bold else FontWeight.Normal,
                        maxLines = 4,
                        modifier = Modifier
                            .width(cellWidth)
                            .border(0.5.dp, MaterialTheme.colorScheme.outlineVariant)
                            .padding(horizontal = 8.dp, vertical = 6.dp)
                    )
                }
            }
            if (rowIndex == 0) {
                HorizontalDivider(thickness = 1.dp, color = MaterialTheme.colorScheme.outline)
            }
        }
    }
}

@Composable
private fun EmptyDoc(modifier: Modifier, message: String) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Text(message, color = MaterialTheme.colorScheme.outline)
    }
}
