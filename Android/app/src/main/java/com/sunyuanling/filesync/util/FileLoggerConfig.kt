package com.sunyuanling.filesync.util

import java.io.File

enum class LogLevel(val priority: Int, val flag: String) {
    VERBOSE(2, "V"),
    DEBUG(3, "D"),
    INFO(4, "I"),
    WARN(5, "W"),
    ERROR(6, "E"),
}

data class FileLoggerConfig(
    val logDir: File,                          // 例如 /storage/emulated/0/FileSync/log
    val fileName: String = "app.log",
    val maxFileSize: Long = 5 * 1024 * 1024,   // 单文件 5MB
    val maxFileCount: Int = 5,                 // 最多保留 5 个文件
    val minLevel: LogLevel = LogLevel.VERBOSE, // release 可设为 INFO
    val alsoLogcat: Boolean = true,            // 同时输出到 logcat
)