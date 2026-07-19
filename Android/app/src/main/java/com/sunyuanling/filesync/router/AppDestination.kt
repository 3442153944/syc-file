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

/**同步列表（同步记录 + 待处理事项）*/
@Serializable data object SyncListDestination

/**同步文件夹映射（本设备本地目录映射）*/
@Serializable data object SyncFolderMapDestination

// 特殊页面
/**权限设置*/
@Serializable data object PermissionDestination
/**登录*/
@Serializable data object LoginDestination

/**
 * 设备监控列表*/

@Serializable data object MonitorListDestination
/**
 * 文件在线预览页面。
 * 携带定位远端文件所需的信息，预览器据 [extension]/[name] 分派到图片/视频/音频/PDF/文本/Office 渲染。
 * @param path 远端文件绝对路径（如 E:\FileSync\a.mp4），类型安全导航会自动做 URL 编码
 * @param name 文件名
 * @param size 文件大小（字节），用于展示与部分渲染决策
 * @param extension 扩展名（不含点，可空串，为空时回退按 name 推断）
 * @param deviceId 归属设备 id（可空串）
 */
@Serializable data class PreviewDestination(
    val path: String,
    val name: String,
    val size: Long = 0,
    val extension: String = "",
    val deviceId: String = ""
)
