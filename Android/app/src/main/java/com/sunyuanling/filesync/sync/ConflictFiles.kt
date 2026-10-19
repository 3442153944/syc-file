package com.sunyuanling.filesync.sync

import java.io.File
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.UUID

/** 冲突内容和完整相对路径一起落盘，进程重启后仍能准确恢复。 */
object ConflictFiles {
    fun resolve(root: File, relative: String): File {
        require(relative.isNotBlank() && !relative.startsWith('/') && !relative.contains('\\')) { "无效的相对路径" }
        require(relative.split('/').none { it == ".." || it == "." || it.isEmpty() }) { "无效的相对路径" }
        val canonicalRoot = root.canonicalFile
        val target = File(root, relative).canonicalFile
        require(target.path.startsWith(canonicalRoot.path + File.separator)) { "路径越界" }
        return target
    }

    fun quarantine(root: File, conflictId: Long, relative: String): File {
        resolve(root, relative)
        // 路径单独存为元数据，避免深层目录编码后超过文件名长度限制。
        val directory = resolve(root, ".syncpending/$conflictId")
        check(directory.isDirectory || directory.mkdirs()) { "无法创建冲突目录" }
        val metadata = File(directory, "relative-path")
        check(!metadata.exists() || metadata.readText() == relative) { "冲突路径不一致" }
        if (!metadata.exists()) metadata.writeText(relative)
        return File(directory, "content")
    }

    fun locate(root: File, conflictId: Long): Pair<File, String>? {
        val directory = resolve(root, ".syncpending/$conflictId")
        val content = File(directory, "content")
        val metadata = File(directory, "relative-path")
        if (content.isFile && metadata.isFile) {
            val relative = metadata.readText()
            resolve(root, relative)
            return content to relative
        }
        // 旧格式没有相对路径，保留原副本供手动恢复，不猜测目标目录。
        return null
    }

    fun discard(content: File) {
        check(!content.exists() || content.delete()) { "无法清理冲突副本" }
        if (content.name == "content") {
            File(content.parentFile, "relative-path").delete()
            content.parentFile?.delete()
        }
    }

    fun restore(root: File, content: File, relative: String): File {
        val target = resolve(root, relative)
        val parent = requireNotNull(target.parentFile)
        check(parent.isDirectory || parent.mkdirs()) { "无法创建冲突文件父目录" }
        val temporary = resolve(root, ".synctmp/${UUID.randomUUID()}.tmp")
        val temporaryParent = requireNotNull(temporary.parentFile)
        check(temporaryParent.isDirectory || temporaryParent.mkdirs()) { "无法创建同步临时目录" }
        try {
            content.copyTo(temporary)
            Files.move(temporary.toPath(), target.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            return target
        } finally {
            temporary.delete()
        }
    }
}
