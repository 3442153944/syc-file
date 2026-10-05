// previewUtil/LocalFileResolver.kt
// 职责：给定服务端上的一个文件，找出本机是否已经有同一份，有就返回本地 File。
// 预览据此「本地优先」：本机已有就直接看本地的，不再走网络。
//
// 两路来源（都只认「文件存在且大小一致」，大小不对说明本地是旧版本或未下完，不能当作同一份）：
//  1. 同步文件夹：服务端路径落在同步文件夹的远端目录下，按相对路径映射到本机的同步目录
//  2. 本机下载记录：TransferPathStore 里记过的本地落盘位置
package com.sunyuanling.filesync.previewUtil

import com.sunyuanling.filesync.sync.SyncEngine
import com.sunyuanling.filesync.sync.SyncMappingStore
import com.sunyuanling.filesync.util.TransferPathStore
import java.io.File

object LocalFileResolver {

    /**
     * @param remotePath 与下载接口的 path 参数同义：可能是目录，也可能已经是文件的完整路径
     * @param expectedSize 期望大小（字节）；<=0 表示未知，此时只要求文件存在且非空
     * @return 本机已有的同一份文件，没有则 null。会读磁盘，调用方应在 IO 线程。
     */
    fun resolve(remotePath: String, name: String, expectedSize: Long): File? {
        val fullRemote = fullRemotePath(remotePath, name)
        return fromSyncFolder(fullRemote, expectedSize)
            ?: TransferPathStore.localPathsFor(remotePath, name)
                .asSequence()
                .map { File(it) }
                .firstOrNull { sameFile(it, expectedSize) }
    }

    /** 与服务端下载接口一致：path 的末段就是文件名则 path 即完整路径，否则 path 是目录。 */
    internal fun fullRemotePath(remotePath: String, name: String): String {
        val p = remotePath.replace('\\', '/').trimEnd('/')
        return if (p.substringAfterLast('/') == name) p else "$p/$name"
    }

    private fun fromSyncFolder(fullRemote: String, expectedSize: Long): File? {
        val folder = SyncEngine.folder.value ?: return null
        val mapping = SyncMappingStore.enabledMappingFor(folder.id) ?: return null
        val root = folder.remotePath.replace('\\', '/').trimEnd('/')
        if (root.isEmpty() || !fullRemote.startsWith("$root/")) return null
        val rel = fullRemote.removePrefix("$root/")
        return File(mapping.localPath, rel).takeIf { sameFile(it, expectedSize) }
    }

    private fun sameFile(file: File, expectedSize: Long): Boolean =
        file.isFile && file.length() > 0 && (expectedSize <= 0 || file.length() == expectedSize)
}
