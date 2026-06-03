package com.example.filesync.router

import kotlinx.serialization.Serializable

// 主 Tab
@Serializable data object HomeDestination
@Serializable data object FilesDestination
@Serializable data object MonitorDestination
@Serializable data object PersonalDestination

// 文件相关
@Serializable data object TransferDestination
@Serializable data class FileDetailDestination(val fileId: String)
@Serializable data object FileUploadDestination
@Serializable data object FileSearchDestination

// 设置相关
@Serializable data object SettingsDestination
@Serializable data object ServerSettingsDestination
@Serializable data object SyncSettingsDestination
@Serializable data object AboutDestination
@Serializable data object TransferSettingsDestination
@Serializable data object LogSettingsDestination
@Serializable data object FileSettingsDestination


// 特殊页面
@Serializable data object PermissionDestination
@Serializable data object LoginDestination