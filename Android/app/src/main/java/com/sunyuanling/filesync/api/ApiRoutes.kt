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

    /** POST /v1/file/upload/init —— 分片上传：提交描述信息，建/续会话 */
    const val FILE_UPLOAD_INIT = "/file/upload/init"

    /** POST /v1/file/upload/chunk —— 分片上传：上传单个分片（裸字节 body，query 带 upload_id+index） */
    const val FILE_UPLOAD_CHUNK = "/file/upload/chunk"

    /** GET /v1/file/upload/status —— 分片上传：查缺失分片（断点续传） */
    const val FILE_UPLOAD_STATUS = "/file/upload/status"

    /** POST /v1/file/upload/complete —— 分片上传：收齐后校验落盘 */
    const val FILE_UPLOAD_COMPLETE = "/file/upload/complete"

    /** POST /v1/file/download-history */
    const val FILE_DOWNLOAD_HISTORY = "/file/download-history"

    /** GET /v1/ws/connect */
    const val WS_CONNECT = "/ws/connect"

    /** GET /v1/ws/online */
    const val WS_ONLINE = "/ws/online"

    /** GET /v1/ws/my-devices （当前用户自己的在线设备） */
    const val WS_MY_DEVICES = "/ws/my-devices"

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
    /**删除下载记录
     * post /v1/file/delete-download-history
     * */
    const val FILE_DELETE_DOWNLOAD_HISTORY = "/file/delete-download-history"

    /** POST /v1/file/delete —— 删除远端单个文件（同步覆盖上传前置） */
    const val FILE_DELETE = "/file/delete"

    // ==================== 同步（internal/sync/router.go） ====================

    /** POST /v1/sync/notify —— 上报单文件变更（file_changed 的 HTTP 通道） */
    const val SYNC_NOTIFY = "/sync/notify"

    /** POST /v1/sync/scan —— 上报全量扫描清单，服务端比对 trunk 补任务 */
    const val SYNC_SCAN = "/sync/scan"

    /** POST /v1/sync/tasks/:id/complete —— 任务完成回报 */
    const val SYNC_TASK_COMPLETE = "/sync/tasks/%s/complete"

    /** POST /v1/sync/tasks/:id/failed —— 任务失败回报 */
    const val SYNC_TASK_FAILED = "/sync/tasks/%s/failed"

    /** POST /v1/sync/tasks/:id/blocked —— 目标被占用，转 waiting_unlock */
    const val SYNC_TASK_BLOCKED = "/sync/tasks/%s/blocked"

    /** GET /v1/sync/tasks —— 同步任务记录（query：status/device_id/limit） */
    const val SYNC_TASKS = "/sync/tasks"

    /** GET /v1/sync/tasks/pending —— 本设备待执行任务（query：device_id 必填） */
    const val SYNC_TASKS_PENDING = "/sync/tasks/pending"

    /** GET /v1/sync/conflicts —— 待处理冲突列表 */
    const val SYNC_CONFLICTS = "/sync/conflicts"

    /** POST /v1/sync/conflicts/:id/resolve —— 解决冲突（body: {resolution}） */
    const val SYNC_CONFLICT_RESOLVE = "/sync/conflicts/%s/resolve"

    /** DELETE /v1/sync/conflicts/:id —— 删除冲突记录 */
    const val SYNC_CONFLICT_DELETE = "/sync/conflicts/%s"

    /** GET /v1/sync/folder —— 该账号唯一的同步文件夹配置（可能为 null） */
    const val SYNC_FOLDER = "/sync/folder"

    // ==================== 应用更新（internal/update/router.go） ====================

    /** GET /v1/update/check —— 检查更新（query：platform + version_code） */
    const val UPDATE_CHECK = "/update/check"

    /** GET /v1/update/latest —— 最新上架版本（query：platform） */
    const val UPDATE_LATEST = "/update/latest"
}
