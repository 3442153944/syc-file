package com.sunyuanling.filesync

import com.sunyuanling.filesync.sync.ConflictFiles
import com.sunyuanling.filesync.util.UploadStaging
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.ByteArrayInputStream
import java.io.File
import java.io.IOException

class FileSafetyTest {
    @get:Rule val temporary = TemporaryFolder()

    @Test fun sameNamesHaveIndependentContentAndCleanup() {
        val root = temporary.newFolder("uploads")
        val first = UploadStaging.copy(root, "same.txt", ByteArrayInputStream("first".toByteArray()))
        val second = UploadStaging.copy(root, "same.txt", ByteArrayInputStream("second".toByteArray()))
        assertNotEquals(first.absolutePath, second.absolutePath)
        assertEquals("same.txt", first.name)
        assertEquals("first", first.readText())
        assertEquals("second", second.readText())
        UploadStaging.remove(root, first.absolutePath)
        assertFalse(first.exists())
        assertTrue(second.exists())
        val outside = temporary.newFile("outside.txt")
        UploadStaging.remove(root, outside.absolutePath)
        assertTrue(outside.exists())
    }

    @Test fun failedImportCleansPartialFile() {
        val root = temporary.newFolder("failed")
        val input = object : java.io.InputStream() {
            override fun read(): Int = throw IOException("interrupted read")
        }
        assertThrows(IOException::class.java) { UploadStaging.copy(root, "broken.txt", input) }
        assertTrue(root.listFiles().orEmpty().isEmpty())
        assertThrows(IllegalArgumentException::class.java) {
            UploadStaging.copy(root, "../escape.txt", ByteArrayInputStream(byteArrayOf()))
        }
    }

    @Test fun nestedConflictRestoresExactPathAndKeepsBackupUntilConfirmed() {
        val root = temporary.newFolder("sync")
        val relative = "docs/nested/report.txt"
        val quarantine = ConflictFiles.quarantine(root, 42, relative)
        quarantine.writeText("local change")
        // 重新从磁盘定位，模拟进程重启后的恢复。
        val (backup, storedPath) = ConflictFiles.locate(root, 42)!!
        assertEquals(relative, storedPath)
        val serverCopy = File(root, relative)
        requireNotNull(serverCopy.parentFile).mkdirs()
        serverCopy.writeText("server copy")
        val target = ConflictFiles.restore(root, backup, storedPath)
        assertEquals(File(root, relative).canonicalFile, target)
        assertEquals("local change", target.readText())
        assertTrue(backup.exists())
        assertFalse(File(root, "report.txt").exists())
        ConflictFiles.discard(backup)
        assertNull(ConflictFiles.locate(root, 42))
        assertEquals("local change", target.readText())
    }

    @Test fun malformedOrAmbiguousConflictNeverOverwritesRootFile() {
        val root = temporary.newFolder("invalid")
        assertThrows(IllegalArgumentException::class.java) { ConflictFiles.quarantine(root, 1, "../escape") }
        val legacy = File(root, ".syncpending/7_report.txt")
        requireNotNull(legacy.parentFile).mkdirs()
        legacy.writeText("legacy content")
        assertNull(ConflictFiles.locate(root, 7))
        assertTrue(legacy.exists())
        val backup = ConflictFiles.quarantine(root, 9, "a/report.txt")
        backup.writeText("original backup")
        assertThrows(IllegalStateException::class.java) { ConflictFiles.quarantine(root, 9, "b/report.txt") }
        assertEquals("original backup", backup.readText())
    }
}
