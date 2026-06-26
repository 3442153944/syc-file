package com.sunyuanling.filesync.ui.viewModel.transmission

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import com.sunyuanling.filesync.dataClass.DownloadItem
import com.sunyuanling.filesync.ui.viewModel.data.DownloadController
import kotlinx.coroutines.flow.StateFlow
import java.io.File

/**
 * 下载列表 ViewModel（薄壳）。状态与逻辑收敛在进程级 [DownloadController] 单例，
 * 保证文件页与传输页（不同 NavBackStackEntry 的 ViewModel 实例）共享同一份下载列表。
 */
class DownloadListViewModel(application: Application) : AndroidViewModel(application) {

    val downloads: StateFlow<List<DownloadItem>> = DownloadController.downloads

    val downloadList: List<DownloadItem>
        get() = DownloadController.downloads.value

    fun addDownload(
        path: String,
        name: String,
        saveDir: File,
        deviceId: String? = null
    ) = DownloadController.addDownload(path, name, saveDir, deviceId)

    fun pauseDownload(downloadId: String) =
        DownloadController.pauseDownload(downloadId)

    fun resumeDownload(downloadId: String) =
        DownloadController.resumeDownload(downloadId)

    fun cancelDownload(downloadId: String) =
        DownloadController.cancelDownload(downloadId)

    fun retryDownload(downloadId: String) =
        DownloadController.retryDownload(downloadId)

    fun removeDownload(downloadId: String) =
        DownloadController.removeDownload(downloadId)
}
