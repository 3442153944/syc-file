package com.sunyuanling.filesync.util

// util/DateUtil.kt
import android.os.Build
import androidx.annotation.RequiresApi
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter

/**
 * 格式化 ISO 时间字符串
 * @param pattern 格式，如 "yyyy-MM-dd HH:mm:ss"、"yyyy-MM-dd"、"HH:mm"
 * @param value ISO 时间字符串，支持 "2026-02-20T18:49:06.403512Z" 等格式
 * @return 格式化后的本地时间字符串，解析失败返回原值
 */
@RequiresApi(Build.VERSION_CODES.O)
fun formatDate(pattern: String, value: String): String {
    return try {
        val parsed = ZonedDateTime.parse(value)
            .withZoneSameInstant(ZoneId.systemDefault())
        parsed.format(DateTimeFormatter.ofPattern(pattern))
    } catch (e: Exception) {
        value
    }
}

/**
 * 格式化时间戳为可读时间
 */
@RequiresApi(Build.VERSION_CODES.O)
fun formatDate(pattern: String, millis: Long): String {
    if (millis <= 0) return ""
    return try {
        val instant = Instant.ofEpochMilli(millis)
        val zdt = instant.atZone(ZoneId.systemDefault())
        zdt.format(DateTimeFormatter.ofPattern(pattern))
    } catch (e: Exception) {
        ""
    }
}