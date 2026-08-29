package com.sunyuanling.filesync.ui.components.serverSetting

import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.sunyuanling.filesync.AppConfig
import com.sunyuanling.filesync.service.SyncKeepAliveService
import com.sunyuanling.filesync.util.RootHelper

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SyncSettingsScreen(navController: NavController) {
    val context = LocalContext.current
    var autoSync by remember { mutableStateOf(AppConfig.autoSyncEnabled) }
    var intervalMinutes by remember { mutableIntStateOf((AppConfig.autoSyncIntervalMs / 60_000).toInt()) }
    var wifiOnly by remember { mutableStateOf(AppConfig.syncOnWifiOnly) }
    var persistentDownload by remember { mutableStateOf(AppConfig.persistentDownloadEnabled) }
    var forceKeepAlive by remember { mutableStateOf(AppConfig.forceKeepAliveEnabled) }

    // 持久化续传开关仅 Root 模式下可见
    var isRooted by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        isRooted = RootHelper.isDeviceRooted() && RootHelper.checkRootAccess()
    }

    // 是否已在电池优化白名单
    val powerManager = remember { context.getSystemService(PowerManager::class.java) }
    var ignoringBattery by remember {
        mutableStateOf(powerManager?.isIgnoringBatteryOptimizations(context.packageName) == true)
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("同步设置") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    TextButton(onClick = {
                        AppConfig.autoSyncEnabled = autoSync
                        AppConfig.autoSyncIntervalMs = intervalMinutes * 60_000L
                        AppConfig.syncOnWifiOnly = wifiOnly
                        AppConfig.persistentDownloadEnabled = persistentDownload
                        val keepAliveChanged = AppConfig.forceKeepAliveEnabled != forceKeepAlive
                        AppConfig.forceKeepAliveEnabled = forceKeepAlive
                        ConfigManager.save()
                        if (keepAliveChanged) {
                            if (forceKeepAlive) SyncKeepAliveService.start(context)
                            else SyncKeepAliveService.stop(context)
                        }
                    }) { Text("保存") }
                }
            )
        }
    ) { innerPadding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
                .padding(16.dp)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            Card(modifier = Modifier.fillMaxWidth()) {
                Column {
                    SettingsSwitchItem(
                        label = "启用自动同步",
                        checked = autoSync,
                        onCheckedChange = { autoSync = it }
                    )
                    HorizontalDivider(modifier = Modifier.padding(horizontal = 16.dp))
                    SettingsSwitchItem(
                        label = "仅 Wi-Fi 下同步",
                        subtitle = "移动网络下不触发自动同步",
                        checked = wifiOnly,
                        onCheckedChange = { wifiOnly = it },
                        enabled = autoSync
                    )
                }
            }
            // 同步间隔（仅自动同步开启时可调）
            if (autoSync) {
                SettingsSliderItem(
                    label = "同步间隔",
                    subtitle = "自动同步触发频率",
                    value = intervalMinutes,
                    range = 1..60,
                    unit = " 分钟",
                    onValueChange = { intervalMinutes = it }
                )
            }

            // 强制保活
            HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp))
            Card(modifier = Modifier.fillMaxWidth()) {
                Column {
                    SettingsSwitchItem(
                        label = "强制保活",
                        subtitle = if (isRooted)
                            "前台服务常驻通知，退后台仍保持同步连接；Root 设备可另挂 LSPosed 看门狗模块守护进程"
                        else
                            "前台服务常驻通知，退后台仍保持同步连接；建议同时加入电池优化白名单，否则可能被系统回收",
                        checked = forceKeepAlive,
                        onCheckedChange = { forceKeepAlive = it }
                    )
                    if (forceKeepAlive && !ignoringBattery) {
                        HorizontalDivider(modifier = Modifier.padding(horizontal = 16.dp))
                        TextButton(
                            onClick = {
                                try {
                                    context.startActivity(
                                        Intent(
                                            Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                                            Uri.parse("package:${context.packageName}")
                                        )
                                    )
                                } catch (_: Exception) {
                                    // 个别 ROM 不支持该 action，退回电池优化总列表
                                    runCatching {
                                        context.startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                                    }
                                }
                                ignoringBattery =
                                    powerManager?.isIgnoringBatteryOptimizations(context.packageName) == true
                            },
                            modifier = Modifier.padding(horizontal = 8.dp)
                        ) { Text("加入电池优化白名单（当前未加入）", fontSize = 13.sp) }
                    } else if (forceKeepAlive) {
                        Text(
                            text = "已在电池优化白名单",
                            fontSize = 12.sp,
                            color = MaterialTheme.colorScheme.outline,
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)
                        )
                    }
                }
            }

            // 持久化续传（仅 Root 模式可见）
            if (isRooted) {
                HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp))
                Card(modifier = Modifier.fillMaxWidth()) {
                    Column {
                        SettingsSwitchItem(
                            label = "持久化续传（Root）",
                            subtitle = "未完成任务本地持久化，app 结束/重启后自动恢复续传；",
                            checked = persistentDownload,
                            onCheckedChange = { persistentDownload = it }
                        )
                    }
                }
            }
        }
    }
}