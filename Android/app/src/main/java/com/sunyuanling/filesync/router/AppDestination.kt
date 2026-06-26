package com.sunyuanling.filesync.router

import kotlinx.serialization.Serializable

// 主 Tab
/**主页*/
@Serializable data object HomeDestination
/**文件管理*/
@Serializable data object FilesDestination
/**监控*/
@Serializable data object MonitorDestination
/**个人*/
@Serializable data object PersonalDestination

// 文件相关
/**文件传输*/
@Serializable data object TransferDestination
/**文件详情*/
@Serializable data class FileDetailDestination(val fileId: String)
/**文件上传*/
@Serializable data object FileUploadDestination
/**文件搜索*/
@Serializable data object FileSearchDestination

// 设置相关
/**设置*/
@Serializable data object SettingsDestination
/**服务器设置*/
@Serializable data object ServerSettingsDestination
/**同步设置*/
@Serializable data object SyncSettingsDestination
/**关于*/
@Serializable data object AboutDestination
/**传输设置*/
@Serializable data object TransferSettingsDestination
/**日志设置*/
@Serializable data object LogSettingsDestination
/**文件设置*/
@Serializable data object FileSettingsDestination

/**文件传输列表*/
@Serializable data object TransferListDestination

// 特殊页面
/**权限设置*/
@Serializable data object PermissionDestination
/**登录*/
@Serializable data object LoginDestination

/**
 * 设备监控列表*/

@Serializable data object MonitorListDestination
