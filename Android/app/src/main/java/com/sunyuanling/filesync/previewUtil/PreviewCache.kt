// previewUtil/PreviewCache.kt
// 职责：把远端文件流式下载到 app 私有缓存目录（cacheDir/preview），供 PDF/文本/Office 渲染；
//       预览结束由调用方删除（"预览的文件放硬盘、看完即删"）。
// 复用 Request 单例的 OkHttp client 与 FileApi.buildDownloadUrl（token 拼 query）。
package com.sunyuanling.filesync.previewUtil

import android.content.Context
import com.sunyuanling.filesync.api.file.DownloadParams
import com.sunyuanling.filesync.api.file.FileApi
import com.sunyuanling.filesync.network.Request
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import okhttp3.Request as OkRequest
import java.io.File
import kotlin.coroutines.coroutineContext

object PreviewCache {

    /** 预览缓存目录：<cacheDir>/preview。 */
    private fun cacheDir(context: Context): File =
        File(context.cacheDir, "preview").apply { if (!exists()) mkdirs() }

    /**
     * 生成一个稳定但唯一的缓存文件路径。用 path+name 的 hash 前缀避免不同文件重名，
     * 保留原文件名后缀（PdfRenderer / 文本读取 / POI 都不依赖后缀，仅便于调试）。
     */
    private fun cacheFileFor(context: Context, path: String, name: String): File {
        val key = (path + "|" + name).hashCode().toUInt().toString(16)
        val safeName = name.replace(Regex("[^A-Za-z0-9._-]"), "_").takeLast(64)
        return File(cacheDir(context), "${key}_$safeName")
    }

    /**
     * 流式下载远端文件到缓存目录。
     * @param onProgress (已下载字节, 总字节 or -1) 回调，用于进度条。
     * @return 成功返回本地 File；失败返回 Result.failure。协程取消时会删除半成品并抛 CancellationException。
     */
    suspend fun downloadToCache(
        context: Context,
        path: String,
        name: String,
        deviceId: String = "",
        onProgress: (downloaded: Long, total: Long) -> Unit = { _, _ -> }
    ): Result<File> = withContext(Dispatchers.IO) {
        val target = cacheFileFor(context, path, name)
        // 命中已存在且非空的缓存（同一次预览可能重复触发），直接复用
        if (target.exists() && target.length() > 0) {
            onProgress(target.length(), target.length())
            return@withContext Result.success(target)
        }
        val tmp = File(target.absolutePath + ".part")
        try {
            val url = FileApi.buildDownloadUrl(DownloadParams(path = path, name = name, deviceId = deviceId))
            val token = Request.getToken()
            val builder = OkRequest.Builder().url(url).get()
            token?.let { builder.header("Token", it) }

            Request.client.newCall(builder.build()).execute().use { resp ->
                if (!resp.isSuccessful) {
                    return@withContext Result.failure(Exception("下载失败：HTTP ${resp.code}"))
                }
                val body = resp.body ?: return@withContext Result.failure(Exception("下载失败：响应为空"))
                val total = body.contentLength()
                body.byteStream().use { input ->
                    tmp.outputStream().use { output ->
                        val buf = ByteArray(64 * 1024)
                        var downloaded = 0L
                        while (true) {
                            coroutineContext.ensureActive() // 协程取消即中断
                            val read = input.read(buf)
                            if (read == -1) break
                            output.write(buf, 0, read)
                            downloaded += read
                            onProgress(downloaded, total)
                        }
                        output.flush()
                    }
                }
            }
            if (!tmp.renameTo(target)) {
                // 极少数文件系统 rename 失败，退化为拷贝
                tmp.copyTo(target, overwrite = true)
                tmp.delete()
            }
            Result.success(target)
        } catch (e: Exception) {
            tmp.delete()
            if (e is kotlinx.coroutines.CancellationException) throw e
            Result.failure(e)
        }
    }

    /** 删除单个缓存文件（含可能残留的 .part）。 */
    fun delete(file: File?) {
        if (file == null) return
        runCatching { if (file.exists()) file.delete() }
        runCatching { File(file.absolutePath + ".part").let { if (it.exists()) it.delete() } }
    }

    /** 清空整个预览缓存目录（可在进入预览或退出应用时兜底调用）。 */
    fun clearAll(context: Context) {
        runCatching { cacheDir(context).listFiles()?.forEach { it.delete() } }
    }
}
