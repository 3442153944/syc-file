package com.sunyuanling.filesync.util

// util/DateUtil.kt
import android.os.Build
import androidx.annotation.RequiresApi
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.util.concurrent.ConcurrentHashMap

/** 缓存 DateTimeFormatter，避免每次 ofPattern 重复解析模式（列表滚动时频繁调用是 GC 大户）。 */
private val fmtCache = ConcurrentHashMap<String, DateTimeFormatter>()

private fun cachedFmt(pattern: String): DateTimeFormatter =
    fmtCache.getOrPut(pattern) { DateTimeFormatter.ofPattern(pattern) }

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
        parsed.format(cachedFmt(pattern))
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
        zdt.format(cachedFmt(pattern))
    } catch (e: Exception) {
        ""
    }
}