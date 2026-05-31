package com.example.rootinstaller.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Android
import androidx.compose.material.icons.filled.Error
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.InsertDriveFile
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import coil.compose.AsyncImage
import com.example.rootinstaller.model.ApkInfo
import com.example.rootinstaller.model.FileItem
import com.example.rootinstaller.model.InstallState

// 快捷跳转的根目录
private val QUICK_PATHS = listOf(
    "存储" to "/storage/emulated/0",
    "根目录" to "/",
    "下载" to "/storage/emulated/0/Download",
    "data" to "/data",
    "sdcard" to "/sdcard",
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen(vm: MainViewModel = viewModel()) {
    val state by vm.state.collectAsState()
    val context = LocalContext.current
    val snackbarHostState = remember { SnackbarHostState() }
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    LaunchedEffect(state.installResult) {
        state.installResult?.let {
            snackbarHostState.showSnackbar(if (it.success) "✓ ${it.message}" else "✗ ${it.message}")
            vm.clearInstallResult()
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbarHostState) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text("Root 安装器", fontWeight = FontWeight.Bold, fontSize = 18.sp)
                        Text(
                            state.currentPath,
                            fontSize = 11.sp,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            fontFamily = FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f)
                        )
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface
                ),
                actions = {
                    OptionChip("降级", state.allowDowngrade, vm::toggleDowngrade)
                    Spacer(Modifier.width(4.dp))
                    OptionChip("覆盖", state.allowReplace, vm::toggleReplace)
                    Spacer(Modifier.width(4.dp))
                    IconButton(onClick = vm::refresh) {
                        Icon(Icons.Default.Refresh, contentDescription = "刷新")
                    }
                }
            )
        }
    ) { paddingValues ->
        Box(Modifier.fillMaxSize().padding(paddingValues)) {
            when (state.rootAvailable) {
                null -> LoadingView("检测 root 权限...")
                false -> NoRootView()
                true -> {
                    Column(Modifier.fillMaxSize()) {
                        // 快捷路径横向滚动栏
                        DefaultInstallerBanner(
                            isDefault = state.isDefaultInstaller,
                            onSetDefault = vm::setAsDefaultInstaller
                        )
                        QuickPathBar(
                            currentPath = state.currentPath,
                            onNavigate = { vm.navigateTo(it) }
                        )
                        HorizontalDivider(thickness = 0.5.dp)

                        state.error?.let { ErrorBanner(it) }

                        if (state.isLoading && state.files.isEmpty()) {
                            LoadingView("读取目录...")
                        } else {
                            LazyColumn(Modifier.fillMaxSize()) {
                                // 返回上级
                                if (state.pathHistory.size > 1) {
                                    item {
                                        NavigateUpRow(onClick = vm::navigateUp)
                                        HorizontalDivider(thickness = 0.5.dp)
                                    }
                                }
                                if (state.files.isEmpty() && !state.isLoading) {
                                    item {
                                        Box(
                                            Modifier.fillMaxWidth().padding(48.dp),
                                            contentAlignment = Alignment.Center
                                        ) {
                                            Text(
                                                "空目录",
                                                color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.4f)
                                            )
                                        }
                                    }
                                }
                                items(state.files, key = { it.path }) { item ->
                                    FileRow(
                                        item = item,
                                        onClick = {
                                            if (item.isDirectory) vm.navigateTo(item.path)
                                            else if (item.isApk) vm.selectApk(context, item)
                                        }
                                    )
                                    HorizontalDivider(
                                        thickness = 0.5.dp,
                                        color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f)
                                    )
                                }
                            }
                        }
                    }

                    // 安装中遮罩
                    if (state.isLoading && state.files.isNotEmpty()) {
                        Box(
                            Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.4f)),
                            contentAlignment = Alignment.Center
                        ) {
                            Card(shape = RoundedCornerShape(16.dp)) {
                                Column(
                                    Modifier.padding(32.dp),
                                    horizontalAlignment = Alignment.CenterHorizontally
                                ) {
                                    CircularProgressIndicator()
                                    Spacer(Modifier.height(16.dp))
                                    Text("安装中...")
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    state.selectedApk?.let { apkInfo ->
        ModalBottomSheet(
            onDismissRequest = vm::dismissApkSheet,
            sheetState = sheetState
        ) {
            ApkDetailSheet(apkInfo = apkInfo, onInstall = vm::install, onDismiss = vm::dismissApkSheet)
        }
    }
}

// ─── 子组件 ────────────────────────────────────────────────────────────────

@Composable
private fun QuickPathBar(currentPath: String, onNavigate: (String) -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp, vertical = 8.dp),
    ) {
        QUICK_PATHS.forEach { (label, path) ->
            val active = currentPath == path
            Box(
                Modifier
                    .clip(RoundedCornerShape(50))
                    .background(
                        if (active) MaterialTheme.colorScheme.primaryContainer
                        else MaterialTheme.colorScheme.surfaceVariant
                    )
                    .clickable { onNavigate(path) }
                    .padding(horizontal = 12.dp, vertical = 6.dp)
            ) {
                Text(
                    label,
                    fontSize = 12.sp,
                    fontWeight = if (active) FontWeight.Bold else FontWeight.Normal,
                    color = if (active) MaterialTheme.colorScheme.onPrimaryContainer
                    else MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            Spacer(Modifier.width(6.dp))
        }
    }
}

@Composable
private fun OptionChip(label: String, checked: Boolean, onCheckedChange: (Boolean) -> Unit) {
    val bg = if (checked) MaterialTheme.colorScheme.primaryContainer
    else MaterialTheme.colorScheme.surfaceVariant
    val textColor = if (checked) MaterialTheme.colorScheme.onPrimaryContainer
    else MaterialTheme.colorScheme.onSurfaceVariant
    Box(
        Modifier
            .clip(RoundedCornerShape(50))
            .background(bg)
            .clickable { onCheckedChange(!checked) }
            .padding(horizontal = 10.dp, vertical = 4.dp)
    ) {
        Text(label, fontSize = 12.sp, color = textColor, fontWeight = FontWeight.Medium)
    }
}

@Composable
private fun NavigateUpRow(onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(Icons.Default.KeyboardArrowUp, null,
            tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(22.dp))
        Spacer(Modifier.width(14.dp))
        Text("..", fontFamily = FontFamily.Monospace,
            color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.Medium)
    }
}

@Composable
private fun FileRow(item: FileItem, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(
            imageVector = when {
                item.isDirectory -> Icons.Default.Folder
                item.isApk -> Icons.Default.Android
                else -> Icons.Default.InsertDriveFile
            },
            contentDescription = null,
            tint = when {
                item.isDirectory -> MaterialTheme.colorScheme.tertiary
                item.isApk -> MaterialTheme.colorScheme.primary
                else -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.4f)
            },
            modifier = Modifier.size(24.dp)
        )
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(
                item.name,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                fontSize = 14.sp,
                fontWeight = if (item.isApk) FontWeight.Medium else FontWeight.Normal
            )
            if (!item.isDirectory && item.size > 0) {
                Text(item.displaySize, fontSize = 11.sp,
                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.45f))
            }
        }
        if (item.isDirectory) {
            Icon(
                Icons.AutoMirrored.Filled.KeyboardArrowRight, null,
                tint = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.3f),
                modifier = Modifier.size(18.dp)
            )
        }
    }
}

@Composable
fun ApkDetailSheet(apkInfo: ApkInfo, onInstall: () -> Unit, onDismiss: () -> Unit) {
    Column(
        Modifier.fillMaxWidth().padding(start = 24.dp, end = 24.dp, bottom = 40.dp)
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (apkInfo.icon != null) {
                AsyncImage(
                    model = apkInfo.icon,
                    contentDescription = null,
                    modifier = Modifier.size(56.dp).clip(RoundedCornerShape(12.dp))
                )
            } else {
                Box(
                    Modifier.size(56.dp).clip(RoundedCornerShape(12.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant),
                    contentAlignment = Alignment.Center
                ) {
                    Icon(Icons.Default.Android, null, modifier = Modifier.size(32.dp))
                }
            }
            Spacer(Modifier.width(16.dp))
            Column {
                Text(apkInfo.appName, fontWeight = FontWeight.Bold, fontSize = 18.sp)
                Text(
                    apkInfo.packageName, fontSize = 11.sp, fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f),
                    maxLines = 1, overflow = TextOverflow.Ellipsis
                )
            }
        }
        Spacer(Modifier.height(20.dp))
        Card(
            colors = CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f)
            ),
            shape = RoundedCornerShape(12.dp)
        ) {
            Column(Modifier.padding(16.dp)) {
                InfoRow("安装包", "${apkInfo.versionName} (${apkInfo.versionCode})")
                if (apkInfo.installedVersionName != null) {
                    Spacer(Modifier.height(8.dp))
                    InfoRow("已安装", "${apkInfo.installedVersionName} (${apkInfo.installedVersionCode})")
                }
                Spacer(Modifier.height(8.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("操作", fontSize = 13.sp,
                        color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f),
                        modifier = Modifier.width(48.dp))
                    Spacer(Modifier.width(12.dp))
                    InstallStateChip(apkInfo.installState)
                }
            }
        }
        Spacer(Modifier.height(24.dp))
        Button(
            onClick = onInstall,
            modifier = Modifier.fillMaxWidth().height(52.dp),
            shape = RoundedCornerShape(12.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = if (apkInfo.installState == InstallState.DOWNGRADE)
                    MaterialTheme.colorScheme.error
                else MaterialTheme.colorScheme.primary
            )
        ) {
            Text(
                when (apkInfo.installState) {
                    InstallState.NEW -> "安装"
                    InstallState.UPGRADE -> "升级安装"
                    InstallState.SAME -> "重复安装"
                    InstallState.DOWNGRADE -> "降级安装"
                },
                fontWeight = FontWeight.Bold, fontSize = 16.sp
            )
        }
    }
}

@Composable
private fun InfoRow(label: String, value: String) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(label, fontSize = 13.sp,
            color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f),
            modifier = Modifier.width(48.dp))
        Spacer(Modifier.width(12.dp))
        Text(value, fontSize = 13.sp, fontWeight = FontWeight.Medium)
    }
}

@Composable
private fun InstallStateChip(state: InstallState) {
    val (bg, fg) = when (state) {
        InstallState.NEW -> MaterialTheme.colorScheme.primaryContainer to MaterialTheme.colorScheme.onPrimaryContainer
        InstallState.UPGRADE -> MaterialTheme.colorScheme.secondaryContainer to MaterialTheme.colorScheme.onSecondaryContainer
        InstallState.SAME -> MaterialTheme.colorScheme.surfaceVariant to MaterialTheme.colorScheme.onSurfaceVariant
        InstallState.DOWNGRADE -> MaterialTheme.colorScheme.errorContainer to MaterialTheme.colorScheme.onErrorContainer
    }
    Box(
        Modifier.clip(RoundedCornerShape(50)).background(bg)
            .padding(horizontal = 10.dp, vertical = 3.dp)
    ) {
        Text(state.label, fontSize = 12.sp, color = fg, fontWeight = FontWeight.Medium)
    }
}

@Composable
private fun NoRootView() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Icon(Icons.Default.Error, null,
                modifier = Modifier.size(48.dp), tint = MaterialTheme.colorScheme.error)
            Spacer(Modifier.height(12.dp))
            Text("需要 Root 权限", fontWeight = FontWeight.Bold, fontSize = 18.sp)
            Spacer(Modifier.height(4.dp))
            Text("请在 KernelSU 中授权本应用",
                color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f))
        }
    }
}

@Composable
private fun LoadingView(text: String = "加载中...") {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            CircularProgressIndicator()
            Spacer(Modifier.height(12.dp))
            Text(text, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f))
        }
    }
}

@Composable
private fun ErrorBanner(message: String) {
    Box(
        Modifier.fillMaxWidth()
            .background(MaterialTheme.colorScheme.errorContainer)
            .padding(12.dp)
    ) {
        Text(message, color = MaterialTheme.colorScheme.onErrorContainer, fontSize = 13.sp)
    }
}

@Composable
private fun DefaultInstallerBanner(isDefault: Boolean, onSetDefault: () -> Unit) {
    if (isDefault) {
        Row(
            Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.primaryContainer)
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                "✓ 已是默认安装器",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.weight(1f)
            )
        }
    } else {
        Row(
            Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.secondaryContainer)
                .padding(horizontal = 16.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                "点击 APK 文件时可能弹出选择器",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
                modifier = Modifier.weight(1f)
            )
            Spacer(Modifier.width(8.dp))
            Box(
                Modifier
                    .clip(RoundedCornerShape(50))
                    .background(MaterialTheme.colorScheme.secondary)
                    .clickable(onClick = onSetDefault)
                    .padding(horizontal = 12.dp, vertical = 4.dp)
            ) {
                Text(
                    "设为默认",
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSecondary,
                    fontWeight = FontWeight.Medium
                )
            }
        }
    }
}