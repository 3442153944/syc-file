// util/StringUtils.kt
package com.sunyuanling.filesync.util

import java.util.Locale
import java.util.regex.Pattern

object StringUtils {

    /**
     * 判断字符串是否为空或只包含空白字符
     */
    fun isBlank(str: String?): Boolean {
        return str.isNullOrBlank()
    }

    /**
     * 判断字符串是否不为空
     */
    fun isNotBlank(str: String?): Boolean {
        return !str.isNullOrBlank()
    }

    /**
     * 验证邮箱格式
     */
    fun isValidEmail(email: String): Boolean {
        val pattern = Pattern.compile(
            "^[A-Za-z0-9+_.-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}$"
        )
        return pattern.matcher(email).matches()
    }

    /**
     * 验证手机号（中国大陆）
     */
    fun isValidPhone(phone: String): Boolean {
        val pattern = Pattern.compile("^1[3-9]\\d{9}$")
        return pattern.matcher(phone).matches()
    }

    /**
     * 截断字符串
     * @param maxLength 最大长度
     * @param suffix 后缀，默认 "..."
     */
    fun truncate(str: String, maxLength: Int, suffix: String = "..."): String {
        return if (str.length <= maxLength) {
            str
        } else {
            str.substring(0, maxLength - suffix.length) + suffix
        }
    }

    /**
     * 生成随机字符串
     * @param length 长度
     */
    fun randomString(length: Int): String {
        val chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
        return (1..length)
            .map { chars.random() }
            .joinToString("")
    }

    /**
     * 首字母大写
     */
    fun capitalize(str: String): String {
        return str.replaceFirstChar { it.uppercase() }
    }

    /**
     * 驼峰转下划线
     * 例如：userName -> user_name
     */
    fun camelToSnake(str: String): String {
        return str.replace(Regex("([a-z])([A-Z])"), "$1_$2").lowercase()
    }

    /**
     * 下划线转驼峰
     * 例如：user_name -> userName
     */
    fun snakeToCamel(str: String): String {
        return str.split('_').mapIndexed { index, s ->
            if (index == 0) s else s.replaceFirstChar { if (it.isLowerCase()) it.titlecase(Locale.ROOT) else it.toString() }
        }.joinToString("")
    }

    /**
     * 隐藏部分字符（用于显示敏感信息）
     * 例如：手机号 138****5678
     */
    fun mask(str: String, start: Int, end: Int, maskChar: Char = '*'): String {
        if (start >= end || start < 0 || end > str.length) {
            return str
        }
        val masked = CharArray(end - start) { maskChar }
        return str.take(start) + String(masked) + str.substring(end)
    }
}