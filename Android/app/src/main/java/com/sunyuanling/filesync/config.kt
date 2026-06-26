package com.sunyuanling.filesync

enum class LogLevel {
    /** 开发者 */
    Debugger,
    /** 详细 */
    Info,
    /** 警告 */
    Warn,
    /** 错误 */
    Error
}

object AppConfig {

    // ==================== 服务器连接 ====================

    /** 服务器域名或 IP */


    var server = "ddns.sunyuanling.cn"
    /** 路由路径 */
    var routePath = ""

    /** 是否启用 HTTPS */
    var isHttps = true

    /** 服务器端口 */
    var port = 8891

    /** HTTP 连接超时（毫秒） */
    var connectTimeoutMs = 30_000

    /** HTTP 读取超时（毫秒） */
    var readTimeoutMs = 30_000

    /** WebSocket 重连间隔（毫秒） */
    var wsReconnectIntervalMs = 5_000L

    /** WebSocket 最大重连次数，-1 表示无限重连 */
    var wsMaxReconnectAttempts = -1

    // ==================== 文件传输 ====================

    /** 上传分片大小（字节），默认 4MB */
    var uploadChunkSize = 4 * 1024 * 1024

    /** 最大并发上传任务数 */
    var maxConcurrentUploads = 3

    /** 最大并发下载任务数 */
    var maxConcurrentDownloads = 3

    /** 下载文件的本地保存根目录（相对 External Storage） */
    var downloadDir = "FileSync/downloads"

    // ==================== 同步 ====================

    /** 是否启用自动同步 */
    var autoSyncEnabled = true

    /** 自动同步间隔（毫秒），默认 5 分钟 */
    var autoSyncIntervalMs = 5 * 60 * 1000L

    /** 同步时是否只在 Wi-Fi 下进行 */
    var syncOnWifiOnly = true

    /**
     * 持久化续传总开关（默认关）。
     * 开启后：未完成下载任务持久化到本地，app 被结束/重启后自动恢复续传
     * （基于 PRDownloader 的 resume DB）。仅 Root 模式下在设置页可见。
     * 这也是后续"定时同步/文件夹实时同步"引擎的地基。
     */
    var persistentDownloadEnabled = false

    // ==================== 日志 ====================

    /** 本地日志记录级别 */
    var loggerLevel = LogLevel.Debugger

    /** 日志文件保存目录（相对 External Storage） */
    var logDir = "FileSync/log"

    /** 单个日志文件最大大小（字节），默认 10MB */
    var logMaxFileSizeBytes = 10 * 1024 * 1024L

    /** 最多保留日志文件数量 */
    var logMaxFileCount = 5

    // ==================== 文件浏览 ====================

    /** root 模式下默认起始目录是否为用户空间（而非 / 根目录） */
    var isUserRoot = true

    /** 文件列表每页加载数量 */
    var filePageSize = 50

    // ==================== 其他 ====================

    /** 应用版本名，用于关于页展示（BuildConfig 在运行时注入） */
    var versionName = "1.0.0"

    // ==================== 工具函数 ====================

    /** 完整服务器 URL，如 https://ddns.sunyuanling.cn:8891 */
    fun getBaseUrl(): String {
        val protocol = if (isHttps) "https" else "http"
        return "$protocol://$server:$port${routePath.trimEnd('/')}"
    }

    fun getWsUrl(path: String = "/file/v1/ws/connect"): String {
        val protocol = if (isHttps) "wss" else "ws"
        return "$protocol://$server:$port$path"
    }
}