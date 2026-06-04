// ui/screen/permission/PermissionScreen.kt
package com.sunyuanling.filesync.ui.screen.permission

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sunyuanling.filesync.util.PermissionHelper
import com.sunyuanling.filesync.util.RootHelper
import com.sunyuanling.filesync.util.rememberPermissionState
import kotlinx.coroutines.launch

/**
 * 权限申请页面
 */
@Composable
fun PermissionScreen(
    onPermissionsGranted: () -> Unit
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var initialCheck by remember { mutableStateOf<PermissionCheckResult?>(null) }
    var rootGranted by remember { mutableStateOf(false) }

    // 初始权限检查
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

            rootGranted = hasRootAccess
            initialCheck = PermissionCheckResult(
                hasBasicPermissions = hasBasicPermissions,
                hasManageStorage = hasManageStorage,
                isRooted = isRooted,
                hasRootAccess = hasRootAccess
            )
        }
    }

    initialCheck?.let { check ->
        PermissionContent(
            initialCheck = check,
            rootGranted = rootGranted,
            onRootGranted = { rootGranted = it },
            onPermissionsGranted = onPermissionsGranted
        )
    }
}

@Composable
private fun PermissionContent(
    initialCheck: PermissionCheckResult,
    rootGranted: Boolean,
    onRootGranted: (Boolean) -> Unit,
    onPermissionsGranted: () -> Unit
) {
    val scope = rememberCoroutineScope()

    val permissionState = rememberPermissionState(
        initialBasicPermissions = initialCheck.hasBasicPermissions,
        initialManageStorage = initialCheck.hasManageStorage,
        onAllPermissionsGranted = {
            if (!initialCheck.isRooted || rootGranted) {
                onPermissionsGranted()
            }
        }
    )

    // 自动检查是否所有权限都已授予
    LaunchedEffect(permissionState.permissionsGranted, permissionState.manageStorageGranted, rootGranted) {
        if (permissionState.permissionsGranted &&
            permissionState.manageStorageGranted &&
            (!initialCheck.isRooted || rootGranted)) {
            onPermissionsGranted()
        }
    }

    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
            modifier = Modifier.padding(32.dp)
        ) {
            Icon(
                imageVector = Icons.Default.Security,
                contentDescription = null,
                modifier = Modifier.size(64.dp),
                tint = MaterialTheme.colorScheme.primary
            )

            Text(
                "权限申请",
                fontSize = 24.sp,
                style = MaterialTheme.typography.headlineMedium
            )

            Text(
                "应用需要以下权限才能正常运行",
                fontSize = 14.sp,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )

            Spacer(modifier = Modifier.height(8.dp))

            if (!permissionState.permissionsGranted) {
                PermissionCard(
                    title = "基础权限",
                    description = "存储、通讯录、短信、摄像头、麦克风",
                    isGranted = false,
                    onRequest = { permissionState.requestPermissions() }
                )
            }

            if (!permissionState.manageStorageGranted) {
                PermissionCard(
                    title = "文件管理权限",
                    description = "访问所有文件和文件夹",
                    isGranted = false,
                    onRequest = { permissionState.requestManageStorage() }
                )
            }

            if (initialCheck.isRooted && !rootGranted) {
                PermissionCard(
                    title = "Root权限",
                    description = "访问系统级文件和功能",
                    isGranted = false,
                    onRequest = {
                        scope.launch {
                            val granted = RootHelper.requestRootAccess()
                            onRootGranted(granted)
                        }
                    }
                )
            }

            if (permissionState.permissionsGranted &&
                permissionState.manageStorageGranted &&
                (!initialCheck.isRooted || rootGranted)) {
                Spacer(modifier = Modifier.height(8.dp))
                Text(
                    "✓ 所有权限已授予，正在进入应用...",
                    fontSize = 16.sp,
                    color = MaterialTheme.colorScheme.primary
                )
            }
        }
    }
}

@Composable
private fun PermissionCard(
    title: String,
    description: String,
    isGranted: Boolean,
    onRequest: () -> Unit
) {
    ElevatedCard(
        modifier = Modifier.fillMaxWidth()
    ) {
        Column(
            modifier = Modifier.padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                Icon(
                    imageVector = if (isGranted) Icons.Default.CheckCircle else Icons.Default.Info,
                    contentDescription = null,
                    tint = if (isGranted)
                        MaterialTheme.colorScheme.primary
                    else
                        MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium
                )
            }

            Text(
                text = description,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )

            if (!isGranted) {
                Button(
                    onClick = onRequest,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("授予权限")
                }
            }
        }
    }
}

data class PermissionCheckResult(
    val hasBasicPermissions: Boolean,
    val hasManageStorage: Boolean,
    val isRooted: Boolean,
    val hasRootAccess: Boolean
)