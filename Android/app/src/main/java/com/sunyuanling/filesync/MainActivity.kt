package com.sunyuanling.filesync

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.Settings
import android.util.Log
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.annotation.RequiresApi
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.NavGraph.Companion.findStartDestination
import com.downloader.PRDownloader
import com.downloader.PRDownloaderConfig
import com.example.filesync.data.sync.WebSocketManager
import com.sunyuanling.filesync.network.AuthManager
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.ui.components.serverSetting.ConfigManager
import com.sunyuanling.filesync.ui.theme.FileSyncTheme
import com.sunyuanling.filesync.ui.viewModel.data.DownloadController
import com.sunyuanling.filesync.util.FileLogger
import com.sunyuanling.filesync.util.FileLoggerConfig
import com.sunyuanling.filesync.util.PermissionHelper
import com.sunyuanling.filesync.util.RootHelper
import com.sunyuanling.filesync.router.AppNavHost
import com.sunyuanling.filesync.router.HomeDestination
import com.sunyuanling.filesync.router.LoginDestination
import com.sunyuanling.filesync.router.PermissionDestination
import com.sunyuanling.filesync.router.TopLevelDestination
import com.sunyuanling.filesync.ui.components.notice.DownloadNotificationHelper
import com.sunyuanling.filesync.ui.components.update.UpdateDialog
import com.sunyuanling.filesync.service.SyncKeepAliveService
import com.sunyuanling.filesync.sync.SyncEngine
import com.sunyuanling.filesync.update.UpdateController
import kotlinx.coroutines.launch
import java.io.File

class MainActivity : ComponentActivity() {
    @RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        DownloadNotificationHelper.createChannels(this)
        try {
            ConfigManager.init()
        }
        catch(e: Exception){
            Log.w("ConfigManager", "配置文件读取失败: ${e.message}")
            Toast.makeText(this, "配置文件读取失败", Toast.LENGTH_SHORT).show()
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1001)
        }

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            if (!Environment.isExternalStorageManager()) {
                val intent = Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION)
                startActivity(intent)
            }
        }
        Request.init(this)


        val config = PRDownloaderConfig.newBuilder()
            .setDatabaseEnabled(true)
            .setReadTimeout(30_000)
            .setConnectTimeout(30_000)
            .build()
        PRDownloader.initialize(applicationContext, config)
        // 下载控制单例接入 Application Context（WS 观察与通知初始化）
        DownloadController.attach(this)

        enableEdgeToEdge()
        setContent {
            FileSyncTheme {
                AppInitializer()
            }
        }
    }
}

@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@Composable
fun AppInitializer() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var isChecking by remember { mutableStateOf(true) }
    var startDestination by remember { mutableStateOf<Any?>(null) }

    try {
        val logDir = File(Environment.getExternalStorageDirectory(), "FileSync/log")
        FileLogger.init(FileLoggerConfig(logDir = logDir))
    } catch (e: Exception) {
        Log.w("FileLogger", "日志初始化失败，等待权限: ${e.message}")
    }

    LaunchedEffect(Unit) {
        scope.launch {
            val hasBasicPermissions = PermissionHelper.hasAllPermissions(context)
            val hasManageStorage = PermissionHelper.hasManageExternalStoragePermission()
            val isRooted = RootHelper.isDeviceRooted()
            val hasRootAccess = if (isRooted) RootHelper.checkRootAccess() else true

            startDestination = when {
                !(hasBasicPermissions && hasManageStorage && hasRootAccess) -> PermissionDestination
                !Request.hasToken() -> LoginDestination
                else -> HomeDestination
            }

            isChecking = false
        }
    }

    when {
        isChecking -> LoadingScreen()
        startDestination != null -> FileSyncApp(startDestination = startDestination!!)
    }
}

@Composable
private fun LoadingScreen() {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            CircularProgressIndicator()
            Text("正在初始化...", fontSize = 16.sp)
        }
    }
}

@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@Composable
fun FileSyncApp(startDestination: Any) {
    val navController = rememberNavController()
    val lifecycleOwner = LocalLifecycleOwner.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    val navBackStackEntry by navController.currentBackStackEntryAsState()
    val currentDestination = navBackStackEntry?.destination

    // ========== 全局路由守卫：监听 401 事件 ==========
    LaunchedEffect(Unit) {
        AuthManager.authEvents.collect { event ->
            when (event) {
                is AuthManager.AuthEvent.TokenExpired -> {
                    Toast.makeText(context, "登录已过期，请重新登录", Toast.LENGTH_SHORT).show()
                    WebSocketManager.disconnect()
                    navController.navigate(LoginDestination) {
                        popUpTo(0) { inclusive = true }
                    }
                }
            }
        }
    }

    // ========== WebSocket 生命周期管理 ==========
    // 强制保活开启时连接归 SyncKeepAliveService 管：ON_STOP 不断开，退后台仍在线
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> scope.launch {
                    if (Request.hasToken()) WebSocketManager.connect(context)
                }
                Lifecycle.Event.ON_STOP -> {
                    if (!AppConfig.forceKeepAliveEnabled) WebSocketManager.disconnect()
                }
                else -> {}
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    // ========== 强制保活服务 + 同步引擎 ==========
    // 键在 currentDestination 上而非 Unit：本 composable 首次进入组合时如果用户还没登录
    // （新装/退出登录后的正常路径，此时页面停在 LoginDestination），Request.hasToken()
    // 当场为 false，Unit-keyed 效果只会跑这一次，之后用户登录成功、导航切到 Home，
    // 这个块永远不会再执行——同步引擎/保活服务/更新检查全部静默不启动。
    // 每次目的地变化都重新看一眼 token，配合下面的 syncStarted 只让真正的启动逻辑跑一次，
    // 保证"登录完成后才第一次拿到 token"这条路径也能正确触发。
    var syncStarted by remember { mutableStateOf(false) }
    LaunchedEffect(currentDestination) {
        if (!syncStarted && Request.hasToken()) {
            syncStarted = true
            if (AppConfig.forceKeepAliveEnabled) {
                SyncKeepAliveService.start(context)
            }
            // autoSyncEnabled 的消费者：任务执行 + 探测上报 + 连接追赶
            if (AppConfig.autoSyncEnabled) {
                SyncEngine.start(context)
            }
            // 应用更新：监听 WS app_update 推送 + 启动时检查一次
            UpdateController.startObserving(context)
            UpdateController.checkForUpdate(context)
        }
    }

    // ========== 是否显示底部导航 ==========
    val hideRoutes = setOf(
        LoginDestination::class,
        PermissionDestination::class
    )
    val shouldShowBottomNav = hideRoutes.none {
        currentDestination?.hasRoute(it) == true
    }

    // ========== UI ==========
    NavigationSuiteScaffold(
        navigationSuiteItems = {
            if (shouldShowBottomNav) {
                TopLevelDestination.entries.forEach { dest ->
                    item(
                        icon = {
                            Icon(
                                imageVector = dest.icon,
                                contentDescription = dest.label
                            )
                        },
                        label = { Text(dest.label) },
                        selected = currentDestination?.hierarchy?.any {
                            it.hasRoute(dest.route)
                        } == true,
                        onClick = {
                            navController.navigate(dest.route.objectInstance!!) {
                                popUpTo(navController.graph.findStartDestination().id) {
                                    saveState = true
                                }
                                launchSingleTop = true
                                restoreState = true
                            }
                        }
                    )
                }
            }
        }
    ) {
        Scaffold(modifier = Modifier.fillMaxSize()) { innerPadding ->
            AppNavHost(
                navController = navController,
                modifier = Modifier.padding(innerPadding),
            )
            // 全局更新提示（有可用更新时弹出，强制更新不可关闭）
            UpdateDialog()
        }
    }
}
