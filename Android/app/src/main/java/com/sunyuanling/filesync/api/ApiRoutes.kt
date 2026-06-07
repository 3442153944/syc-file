// api/ApiRoutes.kt
// 职责：定义全部 API 路由常量，严格对应后端路由表。
// 路径参数（:id、:conn_id、:device_id、:name）以格式化占位符 %s 表示，由调用方传入。
package com.sunyuanling.filesync.api

object ApiRoutes {

    // ==================== 公开路由（无需 Token） ====================

    /** GET /ping —— 根路径（无 /v1 前缀），无法通过 Request 单例调用，需直连 */
    const val PING_ROOT_GET = "/ping"

    /** POST /v1/ping */
    const val PING = "/ping"

    /** POST /v1/user/register */
    const val USER_REGISTER = "/user/register"

    /** POST /v1/user/login */
    const val USER_LOGIN = "/user/login"

    /** POST /v1/user/reset-password */
    const val USER_RESET_PASSWORD = "/user/reset-password"

    /** POST /v1/user/verify */
    const val USER_VERIFY = "/user/verify"

    // ==================== 需鉴权路由 ====================

    /** POST /v1/user/update-info */
    const val USER_UPDATE_INFO = "/user/update-info"

    /** POST /v1/file/available-disks */
    const val FILE_AVAILABLE_DISKS = "/file/available-disks"

    /** POST /v1/file/traverse-directory */
    const val FILE_TRAVERSE_DIRECTORY = "/file/traverse-directory"

    /** GET /v1/file/download */
    const val FILE_DOWNLOAD = "/file/download"

    /** POST /v1/file/upload */
    const val FILE_UPLOAD = "/file/upload"

    /** POST /v1/file/download-history */
    const val FILE_DOWNLOAD_HISTORY = "/file/download-history"

    /** GET /v1/ws/connect */
    const val WS_CONNECT = "/ws/connect"

    /** GET /v1/ws/online */
    const val WS_ONLINE = "/ws/online"

    /** GET /v1/ws/user/:id/connections */
    const val WS_USER_CONNECTIONS = "/ws/user/%s/connections"

    /** GET /v1/ws/stats */
    const val WS_STATS = "/ws/stats"

    /** POST /v1/ws/send */
    const val WS_SEND = "/ws/send"

    /** POST /v1/ws/broadcast */
    const val WS_BROADCAST = "/ws/broadcast"

    /** DELETE /v1/ws/conn/:conn_id */
    const val WS_DISCONNECT_CONN = "/ws/conn/%s"

    /** DELETE /v1/ws/user/:id */
    const val WS_DISCONNECT_USER = "/ws/user/%s"

    /** DELETE /v1/ws/device/:device_id */
    const val WS_DISCONNECT_DEVICE = "/ws/device/%s"

    /** POST /v1/ws/group */
    const val WS_GROUP = "/ws/group"

    /** POST /v1/ws/group/send */
    const val WS_GROUP_SEND = "/ws/group/send"

    /** GET /v1/ws/group/:name/users */
    const val WS_GROUP_USERS = "/ws/group/%s/users"
}
