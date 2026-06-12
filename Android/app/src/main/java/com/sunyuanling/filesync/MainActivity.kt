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
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> scope.launch {
                    if (Request.hasToken()) WebSocketManager.connect()
                }
                Lifecycle.Event.ON_STOP -> WebSocketManager.disconnect()
                else -> {}
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
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
        }
    }
}