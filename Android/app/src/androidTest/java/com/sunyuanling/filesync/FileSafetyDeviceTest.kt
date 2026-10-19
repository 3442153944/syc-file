package com.sunyuanling.filesync

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.sunyuanling.filesync.sync.ConflictFiles
import com.sunyuanling.filesync.util.UploadStaging
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.ByteArrayInputStream
import java.io.File
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class FileSafetyDeviceTest {
    @Test fun sameNameImportAndNestedConflictOnAndroidFilesystem() {
        val cache = InstrumentationRegistry.getInstrumentation().targetContext.cacheDir
        val root = File(cache, "file-safety-${UUID.randomUUID()}").apply { check(mkdir()) }
        try {
            val uploads = File(root, "uploads")
            val one = UploadStaging.copy(uploads, "same.txt", ByteArrayInputStream("one".toByteArray()))
            val two = UploadStaging.copy(uploads, "same.txt", ByteArrayInputStream("two".toByteArray()))
            assertEquals("one", one.readText())
            assertEquals("two", two.readText())
            assertNotEquals(one.absolutePath, two.absolutePath)
            UploadStaging.remove(uploads, one.absolutePath)
            assertTrue(two.exists())

            val sync = File(root, "sync").apply { check(mkdir()) }
            val backup = ConflictFiles.quarantine(sync, 27, "nested/report.txt")
            backup.writeText("local edit")
            val server = File(sync, "nested/report.txt")
            requireNotNull(server.parentFile).mkdirs()
            server.writeText("server edit")
            val stored = requireNotNull(ConflictFiles.locate(sync, 27))
            val restored = ConflictFiles.restore(sync, stored.first, stored.second)
            assertEquals(server.canonicalFile, restored)
            assertEquals("local edit", restored.readText())
            assertTrue(backup.exists())
            assertFalse(File(sync, "report.txt").exists())
            ConflictFiles.discard(backup)
            assertNull(ConflictFiles.locate(sync, 27))
        } finally {
            // 仅清理本测试刚创建的缓存目录。
            check(root.canonicalFile.parentFile == cache.canonicalFile)
            root.deleteRecursively()
        }
    }
}
