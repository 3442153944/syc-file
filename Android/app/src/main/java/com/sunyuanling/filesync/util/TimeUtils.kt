// util/TimeUtils.kt
package com.sunyuanling.filesync.util

import java.text.SimpleDateFormat
import java.util.*

object TimeUtils {

    /**
     * 格式化时间戳为字符串
     * @param timestamp 时间戳（毫秒）
     * @param pattern 格式，默认 "yyyy-MM-dd HH:mm:ss"
     */
    fun format(timestamp: Long, pattern: String = "yyyy-MM-dd HH:mm:ss"): String {
        val sdf = SimpleDateFormat(pattern, Locale.getDefault())
        return sdf.format(Date(timestamp))
    }

    /**
     * 格式化 Date 对象
     */
    fun format(date: Date, pattern: String = "yyyy-MM-dd HH:mm:ss"): String {
        val sdf = SimpleDateFormat(pattern, Locale.getDefault())
        return sdf.format(date)
    }

    /**
     * 获取当前时间戳（毫秒）
     */
    fun now(): Long = System.currentTimeMillis()

    /**
     * 获取当前时间字符串
     */
    fun nowString(pattern: String = "yyyy-MM-dd HH:mm:ss"): String {
        return format(now(), pattern)
    }

    /**
     * 解析时间字符串为时间戳
     */
    fun parse(timeString: String, pattern: String = "yyyy-MM-dd HH:mm:ss"): Long? {
        return try {
            val sdf = SimpleDateFormat(pattern, Locale.getDefault())
            sdf.parse(timeString)?.time
        } catch (e: Exception) {
            null
        }
    }

    /**
     * 计算时间差（返回友好的字符串）
     * 例如：刚刚、1分钟前、2小时前、3天前
     */
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

    /**
     * 格式化时长（毫秒转为可读格式）
     * 例如：1小时23分45秒
     */
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