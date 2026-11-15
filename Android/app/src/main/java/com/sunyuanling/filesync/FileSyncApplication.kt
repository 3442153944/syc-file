package com.sunyuanling.filesync

import android.app.Application
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.share.QuickShareController
import com.sunyuanling.filesync.ui.components.serverSetting.ConfigManager

class FileSyncApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        if (packageName.endsWith(".meshtest")) ConfigManager.useIsolatedTestStorage()
        runCatching { ConfigManager.init() }
        Request.init(this)
        com.sunyuanling.filesync.core.SyncPlanner.init(this)
        QuickShareController.init(this)
    }
}
