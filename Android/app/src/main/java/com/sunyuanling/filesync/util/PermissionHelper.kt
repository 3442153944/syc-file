package com.sunyuanling.filesync.util

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Environment
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import androidx.core.net.toUri

object PermissionHelper {

    // 需要申请的所有危险权限
    val REQUIRED_PERMISSIONS = buildList {
        // 文件权限（Android 13以下）
        if (Build.VERSION.SDK_INT <= Build.VERSION_CODES.S_V2) {
            add(Manifest.permission.READ_EXTERNAL_STORAGE)
            add(Manifest.permission.WRITE_EXTERNAL_STORAGE)
        }
        // 通讯录
        add(Manifest.permission.READ_CONTACTS)
        // 短信
        add(Manifest.permission.READ_SMS)
        add(Manifest.permission.RECEIVE_SMS)
        // 摄像头
        add(Manifest.permission.CAMERA)
        // 麦克风
        add(Manifest.permission.RECORD_AUDIO)
    }.toTypedArray()

    // 检查是否已授予所有权限
    fun hasAllPermissions(context: Context): Boolean {
        return REQUIRED_PERMISSIONS.all {
            ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED
        }
    }

    // 检查是否有文件管理权限（Android 11+）
    fun hasManageExternalStoragePermission(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            true
        }
    }

    // 打开文件管理权限设置页面
    fun openManageExternalStorageSettings(context: Context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val intent = Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION).apply {
                data = "package:${context.packageName}".toUri()
            }
            context.startActivity(intent)
        }
    }
}

@Composable
fun rememberPermissionState(
    initialBasicPermissions: Boolean = false,
    initialManageStorage: Boolean = false,
    onAllPermissionsGranted: () -> Unit = {}
): PermissionState {
    val context = LocalContext.current
    var permissionsGranted by remember { mutableStateOf(initialBasicPermissions) }
    var manageStorageGranted by remember { mutableStateOf(initialManageStorage) }

    // 普通权限申请
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions()
    ) { permissions ->
        permissionsGranted = permissions.all { it.value }
        if (permissionsGranted && manageStorageGranted) {
            onAllPermissionsGranted()
        }
    }

    // 文件管理权限申请（跳转设置页面后返回）
    val manageStorageLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) {
        // 重新检查权限状态
        manageStorageGranted = PermissionHelper.hasManageExternalStoragePermission()
        if (permissionsGranted && manageStorageGranted) {
            onAllPermissionsGranted()
        }
    }

    return remember(permissionsGranted, manageStorageGranted) {
        PermissionState(
            permissionsGranted = permissionsGranted,
            manageStorageGranted = manageStorageGranted,
            requestPermissions = {
                permissionLauncher.launch(PermissionHelper.REQUIRED_PERMISSIONS)
            },
            requestManageStorage = {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    val intent = Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION).apply {
                        data = "package:${context.packageName}".toUri()
                    }
                    manageStorageLauncher.launch(intent)
                }
            }
        )
    }
}

data class PermissionState(
    val permissionsGranted: Boolean,
    val manageStorageGranted: Boolean,
    val requestPermissions: () -> Unit,
    val requestManageStorage: () -> Unit
)