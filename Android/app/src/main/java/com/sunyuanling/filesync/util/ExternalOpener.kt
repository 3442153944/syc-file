// util/ExternalOpener.kt
// 职责：把本机的一个文件交给系统里的其他应用打开（相册、WPS、视频播放器……）。
// 通用工具：只依赖 File，不关心文件从哪来；预览、下载完成后的「打开」等处都能复用。
//
// 经 FileProvider 共享（res/xml/file_paths.xml 里声明了可共享的目录），
// 配合 ACTION_VIEW + 选择器，让用户自己挑用哪个应用。
package com.sunyuanling.filesync.util

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
import java.io.File

object ExternalOpener {

    // 按扩展名猜 MIME；猜不出来就用通配类型，由系统列出所有能处理文件的应用
    fun mimeOf(fileName: String): String {
        val ext = fileName.substringAfterLast('.', "").lowercase()
        return MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext) ?: "*/*"
    }

    /**
     * 用其他应用打开 [file]。
     * @return 失败（没有能处理的应用、文件不可共享等）时返回带可读原因的 Result.failure，调用方自行提示。
     */
    fun open(
        context: Context,
        file: File,
        mimeType: String? = null,
        chooserTitle: String = "用其他应用打开",
    ): Result<Unit> {
        if (!file.isFile) return Result.failure(Exception("文件不存在"))
        return try {
            val uri = uriFor(context, file)
            val view = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, mimeType ?: mimeOf(file.name))
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            val chooser = Intent.createChooser(view, chooserTitle).apply {
                // 选择器自己也要持有读授权，否则部分系统上选完应用后读不到文件
                clipData = ClipData.newRawUri(file.name, uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            context.startActivity(chooser)
            Result.success(Unit)
        } catch (e: ActivityNotFoundException) {
            Result.failure(Exception("没有可以打开该文件的应用"))
        } catch (e: Exception) {
            Result.failure(Exception(e.message ?: "无法打开文件"))
        }
    }

    private fun uriFor(context: Context, file: File): Uri {
        val authority = "${context.packageName}.fileprovider"
        return try {
            FileProvider.getUriForFile(context, authority, file)
        } catch (e: IllegalArgumentException) {
            // 文件不在 FileProvider 声明的目录里（如应用私有 files 目录）：复制一份到缓存目录再共享
            val dir = File(context.cacheDir, "external_open").apply { if (!exists()) mkdirs() }
            val copy = File(dir, file.name)
            file.copyTo(copy, overwrite = true)
            FileProvider.getUriForFile(context, authority, copy)
        }
    }
}
