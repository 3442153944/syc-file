package com.sunyuanling.filesync.util

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.DataOutputStream
import java.io.File

object RootHelper {

    // 检查设备是否已Root
    suspend fun isDeviceRooted(): Boolean = withContext(Dispatchers.IO) {
        val paths = arrayOf(
            "/system/app/Superuser.apk",
            "/sbin/su",
            "/system/bin/su",
            "/system/xbin/su",
            "/data/local/xbin/su",
            "/data/local/bin/su",
            "/system/sd/xbin/su",
            "/system/bin/failsafe/su",
            "/data/local/su",
            "/su/bin/su"
        )

        try {
            paths.any { File(it).exists() }
        } catch (e: SecurityException) {
            false
        } catch (e: Exception) {
            false
        }
    }

    /**
     * 检查是否已拥有Root权限
     * 与 requestRootAccess() 的区别：
     * - checkRootAccess(): 静默检查，不会触发授权弹窗
     * - requestRootAccess(): 主动请求授权，会触发弹窗
     */
    suspend fun checkRootAccess(): Boolean = withContext(Dispatchers.IO) {
        try {
            // 使用 -c 参数执行简单命令，避免交互式shell
            val process = Runtime.getRuntime().exec(arrayOf("su", "-c", "id"))
            val output = process.inputStream.bufferedReader().readText()

            // 等待进程结束，设置超时时间
            val exitCode = withContext(Dispatchers.IO) {
                process.waitFor()
                process.exitValue()
            }

            // 检查命令是否成功执行
            exitCode == 0 && output.contains("uid=0")
        } catch (e: Exception) {
            false
        }
    }

    /**
     * 请求Root权限
     * 会触发授权弹窗（如果之前未授权）
     */
    suspend fun requestRootAccess(): Boolean = withContext(Dispatchers.IO) {
        try {
            val process = Runtime.getRuntime().exec("su")
            val os = DataOutputStream(process.outputStream)
            os.writeBytes("id\n")
            os.writeBytes("exit\n")
            os.flush()
            os.close()

            val output = process.inputStream.bufferedReader().readText()
            process.waitFor()

            process.exitValue() == 0 && output.contains("uid=0")
        } catch (e: Exception) {
            false
        }
    }

    /**
     * 执行Root命令
     */
    suspend fun executeRootCommand(command: String): Result<String> = withContext(Dispatchers.IO) {
        try {
            val process = Runtime.getRuntime().exec("su")
            val os = DataOutputStream(process.outputStream)

            os.writeBytes("$command\n")
            os.writeBytes("exit\n")
            os.flush()
            os.close()

            val output = process.inputStream.bufferedReader().readText()
            val error = process.errorStream.bufferedReader().readText()

            process.waitFor()

            if (process.exitValue() == 0) {
                Result.success(output)
            } else {
                Result.failure(Exception(error.ifEmpty { "Command failed with exit code ${process.exitValue()}" }))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /**
     * 列出指定路径下的所有目录
     */
    suspend fun listDirectories(path: String): List<File> = withContext(Dispatchers.IO) {
        try {
            // 转义路径中的特殊字符
            val escapedPath = path.replace("\"", "\\\"")

            // 使用 find 命令查找所有目录（只查找一级子目录）
            val command = "su -c \"find \\\"$escapedPath\\\" -maxdepth 1 -mindepth 1 -type d\""
            val process = Runtime.getRuntime().exec(arrayOf("sh", "-c", command))

            val output = process.inputStream.bufferedReader().readText()
            val error = process.errorStream.bufferedReader().readText()

            process.waitFor()

            if (process.exitValue() == 0 && output.isNotBlank()) {
                output.lines()
                    .mapNotNull { line ->
                        val trimmed = line.trim()
                        if (trimmed.isNotBlank()) {
                            File(trimmed)
                        } else {
                            null
                        }
                    }
                    .sortedBy { it.name.lowercase() }
            } else {
                if (error.isNotBlank()) {
                    Log.e("RootHelper", "列出目录失败: $error")
                }
                emptyList()
            }
        } catch (e: Exception) {
            Log.e("RootHelper", "列出目录异常", e)
            emptyList()
        }
    }
}