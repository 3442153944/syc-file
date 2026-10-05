// ui/components/preview/FileThumbnail.kt
// 通用组件：文件列表里的「缩略图 / 类型图标」。
//
// 图片 → 缩略图：本机已有同一份就直接用本地文件（不走网络），否则取服务端的缩略图；
// 视频 → 封面（文件自带的封面图，没有就是服务端取的一帧）；有内嵌封面的音频 → 专辑封面
// （/file/thumbnail，几 KB，外网/流量下也不心疼）。点进详情才会去拉原图。
// 非图片、没有路径、加载失败 → 显示调用方给的 [fallback]（通常是类型图标），所以任何文件列表都能无脑套用。
package com.sunyuanling.filesync.ui.components.preview

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import coil.compose.AsyncImagePainter
import coil.request.ImageRequest
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.previewUtil.LocalFileResolver
import com.sunyuanling.filesync.previewUtil.isLocallyDecodableImage
import com.sunyuanling.filesync.previewUtil.isThumbnailable
import com.sunyuanling.filesync.util.AppImageLoader
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * @param path 服务端路径（与下载接口的 path 同义，目录或完整路径均可）；空串表示拿不到路径，直接显示 [fallback]
 * @param size 文件大小（字节）：用于判断本地文件是否为同一份，也作为缩略图缓存版本号
 * @param thumbSize 缩略图方块的边长
 * @param fallback 非图片 / 加载中 / 加载失败时显示的内容（垫在缩略图下面，图片加载出来后自然盖住）
 */
@Composable
fun FileThumbnail(
    path: String,
    name: String,
    size: Long,
    modifier: Modifier = Modifier,
    thumbSize: Dp = 40.dp,
    fallback: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val thumbnailable = path.isNotEmpty() && isThumbnailable(name)

    // 本地优先，没有再用服务端缩略图。读磁盘/查库放 IO 线程；结果为 null 表示还在解析中
    val model by produceState<Any?>(initialValue = null, path, name, size, thumbnailable) {
        value = if (!thumbnailable) null else {
            // 本地优先只对图片：视频/音频的封面本地没法直接解码，直接向服务端要
            val local = if (isLocallyDecodableImage(name)) {
                withContext(Dispatchers.IO) { LocalFileResolver.resolve(path, name, size) }
            } else null
            local ?: FileApi.buildThumbnailUrl(path, name, width = 256, version = size)
        }
    }
    var failed by remember(model) { mutableStateOf(false) }

    Box(
        modifier = modifier
            .size(thumbSize)
            .clip(RoundedCornerShape(6.dp)),
        contentAlignment = Alignment.Center
    ) {
        fallback()
        val m = model
        if (m != null && !failed) {
            AsyncImage(
                model = ImageRequest.Builder(context).data(m).crossfade(true).build(),
                contentDescription = name,
                imageLoader = AppImageLoader.get(context),
                contentScale = ContentScale.Crop,
                onState = { if (it is AsyncImagePainter.State.Error) failed = true },
                modifier = Modifier.fillMaxSize()
            )
        }
    }
}
