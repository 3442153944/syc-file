// util/TimeUtils.kt
package com.sunyuanling.filesync.util

import java.text.SimpleDateFormat
import java.util.*

object TimeUtils {

    /** ThreadLocal 缓存 SimpleDateFormat，避免每次 new（列表滚动 GC 大户）。 */
    private val fmtCache = object : ThreadLocal<MutableMap<String, SimpleDateFormat>>() {
        override fun initialValue(): MutableMap<String, SimpleDateFormat> = HashMap()
    }

    private fun cachedFmt(pattern: String): SimpleDateFormat =
        fmtCache.get().getOrPut(pattern) { SimpleDateFormat(pattern, Locale.getDefault()) }

    fun format(timestamp: Long, pattern: String = "yyyy-MM-dd HH:mm:ss"): String =
        cachedFmt(pattern).format(Date(timestamp))

    fun format(date: Date, pattern: String = "yyyy-MM-dd HH:mm:ss"): String =
        cachedFmt(pattern).format(date)

    fun now(): Long = System.currentTimeMillis()

    fun nowString(pattern: String = "yyyy-MM-dd HH:mm:ss"): String =
        format(now(), pattern)

    fun parse(timeString: String, pattern: String = "yyyy-MM-dd HH:mm:ss"): Long? {
        return try {
            cachedFmt(pattern).parse(timeString)?.time
        } catch (e: Exception) {
            null
        }
    }

    fun timeAgo(timestamp: Long): String {
        val diff = now() - timestamp
        return when {
            diff < 60_000 -> "刚刚"
            diff < 3600_000 -> "${diff / 60_000}分钟前"
            diff < 86400_000 -> "${diff / 3600_000}小时前"
            diff < 2592000_000 -> "${diff / 86400_000}天前"
            else -> format(timestamp, "yyyy-MM-dd")
        }
    }

    fun formatDuration(milliseconds: Long): String {
        val seconds = milliseconds / 1000
        val hours = seconds / 3600
        val minutes = (seconds % 3600) / 60
        val secs = seconds % 60
        return buildString {
            if (hours > 0) append("${hours}小时")
            if (minutes > 0) append("${minutes}分钟")
            if (secs > 0 || isEmpty()) append("${secs}秒")
        }
    }
}
