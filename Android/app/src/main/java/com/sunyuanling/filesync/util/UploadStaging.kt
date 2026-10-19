package com.sunyuanling.filesync.util

import java.io.File
import java.io.InputStream
import java.util.UUID

/** 每次选择都创建独立缓存，同名文件也不会相互覆盖。 */
object UploadStaging {
    fun copy(root: File, name: String, input: InputStream, checkCancelled: () -> Unit = {}): File {
        require(name.isNotBlank() && name !in listOf(".", "..") && name.none { it == '/' || it == '\\' || it.isISOControl() }) { "文件名无效" }
        check(root.isDirectory || root.mkdirs()) { "无法创建上传缓存目录" }
        val directory = File(root, UUID.randomUUID().toString())
        check(directory.mkdir()) { "无法创建上传缓存目录" }
        val target = File(directory, name)
        try {
            target.outputStream().use { out ->
                val buffer = ByteArray(256 * 1024)
                while (true) {
                    checkCancelled()
                    val count = input.read(buffer)
                    if (count < 0) break
                    out.write(buffer, 0, count)
                }
            }
            return target
        } catch (error: Throwable) {
            target.delete()
            directory.delete()
            throw error
        }
    }

    fun remove(root: File, path: String) {
        val file = File(path)
        if (file.canonicalFile.parentFile?.parentFile == root.canonicalFile) {
            file.delete()
            file.parentFile?.delete()
        }
    }
}
