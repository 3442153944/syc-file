// ui/components/preview/ZoomableImage.kt
// 通用组件：应用内查看图片——双指缩放、拖动、双击放大/还原，带加载中与失败状态。
// [model] 可以是本地 File、网络 URL（String）或 Uri，由 Coil 统一加载。
// 网络图片走应用共享的加载器（AppImageLoader，底层 Request.client），会自动带上 Token 与 Device-Id；
// Coil 默认的客户端不带 Device-Id，服务端会按「设备不匹配」拒绝，图片就加载不出来。
package com.sunyuanling.filesync.ui.components.preview

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.IntSize
import coil.ImageLoader
import coil.compose.AsyncImage
import coil.compose.AsyncImagePainter
import coil.request.ImageRequest
import com.sunyuanling.filesync.util.AppImageLoader

private enum class ImageLoadState { LOADING, SUCCESS, ERROR }

/**
 * @param model 本地 File / 网络 URL / Uri
 * @param imageLoader 自定义加载器；默认用应用共享的加载器（走 Request.client，带 Device-Id）
 * @param placeholderModel 原图加载期间先显示的图（一般是缩略图，已在列表里缓存过，几乎立刻出来）；
 *                         原图加载成功后自动让位
 * @param onLoadError 加载失败时回调一次（调用方可据此回退，如本地文件损坏时改看在线版本）
 */
@Composable
fun ZoomableImage(
    model: Any,
    modifier: Modifier = Modifier,
    contentDescription: String? = null,
    imageLoader: ImageLoader? = null,
    placeholderModel: Any? = null,
    maxScale: Float = 6f,
    doubleTapScale: Float = 2.5f,
    onLoadError: (() -> Unit)? = null,
) {
    val context = LocalContext.current
    val loader = imageLoader ?: AppImageLoader.get(context)

    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    var boxSize by remember { mutableStateOf(IntSize.Zero) }
    // 换图（model 变化）后重置状态，否则上一张的缩放/位置会带到下一张
    var loadState by remember(model) { mutableStateOf(ImageLoadState.LOADING) }

    LaunchedEffect(model) {
        scale = 1f
        offset = Offset.Zero
    }
    LaunchedEffect(loadState) {
        if (loadState == ImageLoadState.ERROR) onLoadError?.invoke()
    }

    // 放大后只允许拖到图片边缘贴边，不能把图拖出屏幕
    fun clampOffset(o: Offset, s: Float): Offset {
        val maxX = boxSize.width * (s - 1f) / 2f
        val maxY = boxSize.height * (s - 1f) / 2f
        return Offset(o.x.coerceIn(-maxX, maxX), o.y.coerceIn(-maxY, maxY))
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black)
            .onSizeChanged { boxSize = it }
            .pointerInput(Unit) {
                detectTapGestures(onDoubleTap = {
                    if (scale > 1f) {
                        scale = 1f
                        offset = Offset.Zero
                    } else {
                        scale = doubleTapScale
                        offset = Offset.Zero
                    }
                })
            }
            .pointerInput(Unit) {
                detectTransformGestures { _, pan, zoom, _ ->
                    val newScale = (scale * zoom).coerceIn(1f, maxScale)
                    scale = newScale
                    offset = if (newScale > 1f) clampOffset(offset + pan, newScale) else Offset.Zero
                }
            },
        contentAlignment = Alignment.Center
    ) {
        if (placeholderModel != null && loadState != ImageLoadState.SUCCESS) {
            AsyncImage(
                model = ImageRequest.Builder(context).data(placeholderModel).build(),
                contentDescription = null,
                imageLoader = loader,
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer(
                        scaleX = scale,
                        scaleY = scale,
                        translationX = offset.x,
                        translationY = offset.y
                    )
            )
        }
        AsyncImage(
            model = ImageRequest.Builder(context).data(model).crossfade(true).build(),
            contentDescription = contentDescription,
            imageLoader = loader,
            contentScale = ContentScale.Fit,
            onState = { state ->
                loadState = when (state) {
                    is AsyncImagePainter.State.Success -> ImageLoadState.SUCCESS
                    is AsyncImagePainter.State.Error -> ImageLoadState.ERROR
                    else -> ImageLoadState.LOADING
                }
            },
            modifier = Modifier
                .fillMaxSize()
                .graphicsLayer(
                    scaleX = scale,
                    scaleY = scale,
                    translationX = offset.x,
                    translationY = offset.y
                )
        )
        when (loadState) {
            // 有缩略图占位时画面已经出来了，转圈会盖在上面显得像卡住：只在没有占位时显示
            ImageLoadState.LOADING -> if (placeholderModel == null) CircularProgressIndicator(color = Color.White)
            ImageLoadState.ERROR -> Text(text = "图片加载失败", color = Color.White.copy(alpha = 0.7f))
            ImageLoadState.SUCCESS -> Unit
        }
    }
}
