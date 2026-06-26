package com.sunyuanling.filesync.ui.viewModel.data

import android.os.Environment
import android.util.Log
import com.sunyuanling.filesync.dataClass.DownloadItem
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.File

/**
 * 下载任务持久化。仅当 [com.sunyuanling.filesync.AppConfig.persistentDownloadEnabled]
 * 开启时使用。把未完成下载项写入本地 JSON，app 重启后由 [DownloadController] 读取
 * 并重新 startDownload（PRDownloader 的 resume DB 负责断点续传）。
 *
 * 未来"定时同步/文件夹实时同步"引擎将复用本持久化层。
 */
object DownloadStore {

    private const val TAG = "DownloadStore"

    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
    }

    private val storeFile: File
        get() {
            val dir = File(Environment.getExternalStorageDirectory(), "FileSync")
            if (!dir.exists()) dir.mkdirs()
            return File(dir, "pending_downloads.json")
        }

    fun load(): List<DownloadItem> {
        return try {
            val f = storeFile
            if (!f.exists()) return emptyList()
            val text = f.readText()
            if (text.isBlank()) emptyList()
            else json.decodeFromString(ListSerializer(DownloadItem.serializer()), text)
        } catch (e: Exception) {
            Log.e(TAG, "读取持久化下载失败", e)
            emptyList()
        }
    }

    fun save(items: List<DownloadItem>) {
        try {
            val text = json.encodeToString(ListSerializer(DownloadItem.serializer()), items)
            storeFile.writeText(text)
        } catch (e: Exception) {
            Log.e(TAG, "写入持久化下载失败", e)
        }
    }

    fun clear() {
        try {
            storeFile.delete()
        } catch (e: Exception) {
            Log.e(TAG, "清除持久化下载失败", e)
        }
    }
}
