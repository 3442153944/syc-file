// MainActivity.kt
package com.example.filesync

import android.os.Build
import android.os.Bundle
import android.os.Environment
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
import androidx.navigation.NavHostController
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import com.downloader.PRDownloader
import com.downloader.PRDownloaderConfig
import com.example.filesync.data.sync.WebSocketManager
import com.example.filesync.network.AuthManager
import com.example.filesync.network.Request
import com.example.filesync.router.AppNavHost
import com.example.filesync.router.AppRoute
import com.example.filesync.router.navigateAndClearBackStack
import com.example.filesync.router.navigateToMainTab
import com.example.filesync.ui.theme.FileSyncTheme
import com.example.filesync.util.FileLogger
import com.example.filesync.util.FileLoggerConfig
import com.example.filesync.util.PermissionHelper
import com.example.filesync.util.RootHelper
import kotlinx.coroutines.launch
import java.io.File

class MainActivity : ComponentActivity() {
    @RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 初始化 Request
        Request.init(this)

        // 初始化 PRDownloader
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

/**
 * 应用初始化器
 * 负责权限检查和确定起始路由
 */
@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@Composable
fun AppInitializer() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var isChecking by remember { mutableStateOf(true) }
    var startDestination by remember { mutableStateOf<String?>(null) }

    val logDir = File(Environment.getExternalStorageDirectory(), "FileSync/log")
    FileLogger.init(FileLoggerConfig(logDir = logDir))

    LaunchedEffect(Unit) {
        scope.launch {
            val hasBasicPermissions = PermissionHelper.hasAllPermissions(context)
            val hasManageStorage = PermissionHelper.hasManageExternalStoragePermission()
            val isRooted = RootHelper.isDeviceRooted()
            val hasRootAccess = if (isRooted) {
                RootHelper.checkRootAccess()
            } else {
                true
            }

            startDestination = when {
                !Request.hasToken() -> AppRoute.Login.route
                !(hasBasicPermissions && hasManageStorage && hasRootAccess) ->
                    AppRoute.Permission.route
                else -> AppRoute.Home.route
            }

            isChecking = false
        }
    }

    when {
        isChecking -> {
            LoadingScreen()
        }
        startDestination != null -> {
            FileSyncApp(startDestination = startDestination!!)
        }
    }
}

/**
 * 加载界面
 */
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

/**
 * 主应用界面
 * 包含底部导航栏、路由管理和全局认证守卫
 */
@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@Composable
fun FileSyncApp(startDestination: String) {
    val navController = rememberNavController()
    val lifecycleOwner = LocalLifecycleOwner.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    val navBackStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = navBackStackEntry?.destination?.route

    // ========== 全局路由守卫：监听 401 事件，跳转登录页 ==========
    LaunchedEffect(Unit) {
        AuthManager.authEvents.collect { event ->
            when (event) {
                is AuthManager.AuthEvent.TokenExpired -> {
                    Toast.makeText(context, "登录已过期，请重新登录", Toast.LENGTH_SHORT).show()
                    WebSocketManager.disconnect()
                    navController.navigateAndClearBackStack(AppRoute.Login)
                }
            }
        }
    }

    // ========== WebSocket 生命周期管理 ==========
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> {
                    scope.launch {
                        if (Request.hasToken()) {
                            WebSocketManager.connect()
                        }
                    }
                }
                Lifecycle.Event.ON_STOP -> {
                    WebSocketManager.disconnect()
                }
                else -> {}
            }
        }

        lifecycleOwner.lifecycle.addObserver(observer)

        onDispose {
            lifecycleOwner.lifecycle.removeObserver(observer)
        }
    }

    // ========== UI ==========
    val shouldShowBottomNav = AppRoute.shouldShowBottomNav(currentRoute)

    NavigationSuiteScaffold(
        navigationSuiteItems = {
            if (shouldShowBottomNav) {
                AppRoute.bottomNavRoutes.forEach { item ->
                    item(
                        icon = {
                            Icon(
                                imageVector = item.icon,
                                contentDescription = item.label
                            )
                        },
                        label = { Text(item.label) },
                        selected = currentRoute == item.route.route,
                        onClick = {
                            navController.navigateToMainTab(item.route.route)
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
                startDestination = startDestination
            )
        }
    }
}