// util/FileUtils.kt
package com.sunyuanling.filesync.util

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import java.io.File
import java.io.FileInputStream
import java.security.MessageDigest

object FileUtils {

    /**
     * 格式化文件大小
     * 例如：1.5 MB、2.3 GB
     */
    fun formatSize(bytes: Long): String {
        return when {
            bytes < 1024 -> "$bytes B"
            bytes < 1024 * 1024 -> "%.2f KB".format(bytes / 1024.0)
            bytes < 1024 * 1024 * 1024 -> "%.2f MB".format(bytes / (1024.0 * 1024))
            else -> "%.2f GB".format(bytes / (1024.0 * 1024 * 1024))
        }
    }

    /**
     * 文件分片
     * @param file 要分片的文件
     * @param chunkSize 每片大小（字节），默认 1MB
     * @return 分片列表
     */
    fun splitFile(file: File, chunkSize: Int = 1024 * 1024): List<FileChunk> {
        val chunks = mutableListOf<FileChunk>()
        val fileSize = file.length()
        val totalChunks = ((fileSize + chunkSize - 1) / chunkSize).toInt()

        file.inputStream().use { input ->
            var chunkIndex = 0
            var offset = 0L

            while (offset < fileSize) {
                val currentChunkSize = minOf(chunkSize.toLong(), fileSize - offset).toInt()
                val buffer = ByteArray(currentChunkSize)
                val bytesRead = input.read(buffer, 0, currentChunkSize)

                if (bytesRead > 0) {
                    chunks.add(
                        FileChunk(
                            index = chunkIndex,
                            data = buffer.copyOf(bytesRead),
                            size = bytesRead,
                            offset = offset,
                            totalChunks = totalChunks
                        )
                    )
                    offset += bytesRead
                    chunkIndex++
                }
            }
        }

        return chunks
    }

    /**
     * 计算文件 MD5
     */
    fun calculateMD5(file: File): String {
        val md = MessageDigest.getInstance("MD5")
        FileInputStream(file).use { input ->
            val buffer = ByteArray(8192)
            var bytesRead: Int
            while (input.read(buffer).also { bytesRead = it } != -1) {
                md.update(buffer, 0, bytesRead)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    /**
     * 计算文件 SHA256
     */
    fun calculateSHA256(file: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        FileInputStream(file).use { input ->
            val buffer = ByteArray(8192)
            var bytesRead: Int
            while (input.read(buffer).also { bytesRead = it } != -1) {
                md.update(buffer, 0, bytesRead)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    /**
     * 获取文件扩展名
     */
    fun getExtension(fileName: String): String {
        val lastDot = fileName.lastIndexOf('.')
        return if (lastDot > 0) {
            fileName.substring(lastDot + 1).lowercase()
        } else {
            ""
        }
    }

    /**
     * 根据扩展名获取 MIME 类型
     */
    fun getMimeType(fileName: String): String {
        return when (getExtension(fileName)) {
            "jpg", "jpeg" -> "image/jpeg"
            "png" -> "image/png"
            "gif" -> "image/gif"
            "pdf" -> "application/pdf"
            "txt" -> "text/plain"
            "mp4" -> "video/mp4"
            "mp3" -> "audio/mpeg"
            "zip" -> "application/zip"
            else -> "application/octet-stream"
        }
    }

    /**
     * 从 Uri 获取文件名
     */
    fun getFileName(context: Context, uri: Uri): String? {
        var result: String? = null
        if (uri.scheme == "content") {
            context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
                if (cursor.moveToFirst()) {
                    val index = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                    if (index != -1) {
                        result = cursor.getString(index)
                    }
                }
            }
        }
        if (result == null) {
            result = uri.path?.let { path ->
                val cut = path.lastIndexOf('/')
                if (cut != -1) path.substring(cut + 1) else path
            }
        }
        return result
    }

    /**
     * 从 Uri 获取文件大小
     */
    fun getFileSize(context: Context, uri: Uri): Long {
        var size = 0L
        if (uri.scheme == "content") {
            context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
                if (cursor.moveToFirst()) {
                    val index = cursor.getColumnIndex(OpenableColumns.SIZE)
                    if (index != -1) {
                        size = cursor.getLong(index)
                    }
                }
            }
        } else if (uri.scheme == "file") {
            uri.path?.let { path ->
                size = File(path).length()
            }
        }
        return size
    }

    /**
     * 删除文件或目录（递归删除）
     */
    fun deleteRecursively(file: File): Boolean {
        if (file.isDirectory) {
            file.listFiles()?.forEach { child ->
                deleteRecursively(child)
            }
        }
        return file.delete()
    }
}

/**
 * 文件分片数据类
 */
data class FileChunk(
    val index: Int,         // 分片索引
    val data: ByteArray,    // 分片数据
    val size: Int,          // 分片大小
    val offset: Long,       // 在原文件中的偏移量
    val totalChunks: Int    // 总分片数
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (javaClass != other?.javaClass) return false

        other as FileChunk

        if (index != other.index) return false
        if (!data.contentEquals(other.data)) return false
        if (size != other.size) return false
        if (offset != other.offset) return false
        if (totalChunks != other.totalChunks) return false

        return true
    }

    override fun hashCode(): Int {
        var result = index
        result = 31 * result + data.contentHashCode()
        result = 31 * result + size
        result = 31 * result + offset.hashCode()
        result = 31 * result + totalChunks
        return result
    }
}
/**
 * 格式化文件大小
 */
fun formatFileSize(bytes: Long): String {
    return when {
        bytes < 1024 -> "$bytes B"
        bytes < 1024 * 1024 -> "${bytes / 1024} KB"
        bytes < 1024 * 1024 * 1024 -> "${bytes / (1024 * 1024)} MB"
        else -> "${bytes / (1024 * 1024 * 1024)} GB"
    }
}