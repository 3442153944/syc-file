// util/ServerTime.kt
// 解析服务端（Go time.Time）返回的时间字符串。
// 输出是 RFC3339，可能带 +08:00 之类的时区偏移，也可能是 Z、带不带小数秒；
// OffsetDateTime 一次覆盖这些写法（minSdk 26，可直接用 java.time）。
package com.sunyuanling.filesync.util

import java.time.OffsetDateTime

/** 解析失败或为空返回 0。 */
fun parseServerTime(timeStr: String?): Long {
    if (timeStr.isNullOrBlank()) return 0L
    return try {
        OffsetDateTime.parse(timeStr).toInstant().toEpochMilli()
    } catch (e: Exception) {
        0L
    }
}
