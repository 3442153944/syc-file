// util/AppImageLoader.kt
// 应用级共享的 Coil ImageLoader：网络图片走 Request.client。
//  - 带上 Device-Id：服务端 token 绑定设备，Coil 默认的 OkHttp 客户端不带，会被当作未登录拒绝；
//  - 单例：缩略图列表、预览页共用同一份内存/磁盘缓存，同一张缩略图不会被各处重复下载。
package com.sunyuanling.filesync.util

import android.content.Context
import coil.ImageLoader
import com.sunyuanling.filesync.network.Request

object AppImageLoader {

    @Volatile
    private var loader: ImageLoader? = null

    fun get(context: Context): ImageLoader =
        loader ?: synchronized(this) {
            loader ?: ImageLoader.Builder(context.applicationContext)
                .callFactory { Request.client }
                .crossfade(true)
                .build()
                .also { loader = it }
        }
}
