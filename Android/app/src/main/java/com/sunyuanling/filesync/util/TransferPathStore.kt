// util/TransferPathStore.kt
// 职责：本机的「传输记录 → 文件路径」映射（SQLite）。
//
// 服务端的下载历史（download_history）只记文件名/大小/状态，不记路径，也不该记——
// 路径映射是本设备自己的事。这里在本机发起每次下载时把远端路径、本地保存位置记下来，
// 传输列表里的历史记录再按「文件名 + 时间」匹配回来，点击卡片才知道该预览哪个文件。
// 同步引擎的下载也走这个接口：同步下来的图片同样要能从记录直接预览。
package com.sunyuanling.filesync.util

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import android.util.Log

/** 一条映射：远端路径（预览/重新下载用）+ 本地保存位置（可能为空）。 */
data class TransferPath(
    /** 服务端路径，与下载接口的 path 参数同义（目录或完整路径均可，需配合 name 使用） */
    val remotePath: String,
    val name: String,
    /** 本机落盘的绝对路径，未知为空串 */
    val localPath: String,
    /** 发起时间（毫秒） */
    val createdAt: Long,
)

object TransferPathStore {

    private const val TAG = "TransferPathStore"
    private const val DB_NAME = "transfer_paths.db"
    private const val TABLE = "transfer_path"

    /** 历史记录与本机记录的时间容差：服务端记录的开始时间与本机发起时间相差不会太大。 */
    private const val MATCH_WINDOW_MS = 10 * 60 * 1000L

    /** 记录上限，超出后删最旧的，避免无限增长。 */
    private const val MAX_ROWS = 5000

    @Volatile private var helper: Helper? = null

    fun init(context: Context) {
        if (helper == null) {
            synchronized(this) {
                if (helper == null) helper = Helper(context.applicationContext)
            }
        }
    }

    /** 记一笔本机发起的传输。失败只打日志，不能影响传输本身。 */
    fun record(remotePath: String, name: String, localPath: String, createdAt: Long = System.currentTimeMillis()) {
        val h = helper ?: return
        runCatching {
            val db = h.writableDatabase
            db.insert(TABLE, null, ContentValues().apply {
                put("remote_path", remotePath)
                put("name", name)
                put("local_path", localPath)
                put("created_at", createdAt)
            })
            db.execSQL(
                "DELETE FROM $TABLE WHERE id NOT IN (SELECT id FROM $TABLE ORDER BY created_at DESC LIMIT $MAX_ROWS)"
            )
        }.onFailure { Log.w(TAG, "记录传输路径失败: ${it.message}") }
    }

    /** 按文件名取与 [startedAt] 最接近（且在容差内）的一条；没有则返回 null。 */
    fun find(name: String, startedAt: Long): TransferPath? {
        val h = helper ?: return null
        return runCatching {
            h.readableDatabase.rawQuery(
                "SELECT remote_path, name, local_path, created_at FROM $TABLE " +
                        "WHERE name = ? AND ABS(created_at - ?) <= ? " +
                        "ORDER BY ABS(created_at - ?) ASC LIMIT 1",
                arrayOf(name, startedAt.toString(), MATCH_WINDOW_MS.toString(), startedAt.toString())
            ).use { c ->
                if (c.moveToFirst()) TransferPath(c.getString(0), c.getString(1), c.getString(2), c.getLong(3))
                else null
            }
        }.onFailure { Log.w(TAG, "查询传输路径失败: ${it.message}") }.getOrNull()
    }

    /**
     * 该远端文件在本机落过盘的位置（新的在前，已过滤掉空串）。调用方自行判断文件是否还在。
     * 远端路径可能记成目录、也可能记成完整路径，两种写法都当作候选一起查。
     */
    fun localPathsFor(remotePath: String, name: String): List<String> {
        val h = helper ?: return emptyList()
        val p = remotePath.replace('\\', '/').trimEnd('/')
        val candidates = linkedSetOf(remotePath, p, "$p/$name")
        if (p.substringAfterLast('/') == name) candidates += p.substringBeforeLast('/', "")
        val list = candidates.toList()
        val marks = list.joinToString(",") { "?" }
        return runCatching {
            h.readableDatabase.rawQuery(
                "SELECT local_path FROM $TABLE WHERE name = ? AND local_path != '' AND remote_path IN ($marks) " +
                        "ORDER BY created_at DESC LIMIT 20",
                (listOf(name) + list).toTypedArray()
            ).use { c ->
                buildList { while (c.moveToNext()) add(c.getString(0)) }
            }
        }.onFailure { Log.w(TAG, "查询本地落盘位置失败: ${it.message}") }.getOrDefault(emptyList())
    }

    private class Helper(context: Context) : SQLiteOpenHelper(context, DB_NAME, null, 1) {
        override fun onCreate(db: SQLiteDatabase) {
            db.execSQL(
                "CREATE TABLE $TABLE (" +
                        "id INTEGER PRIMARY KEY AUTOINCREMENT, " +
                        "remote_path TEXT NOT NULL, " +
                        "name TEXT NOT NULL, " +
                        "local_path TEXT NOT NULL DEFAULT '', " +
                        "created_at INTEGER NOT NULL)"
            )
            db.execSQL("CREATE INDEX idx_transfer_path_name ON $TABLE(name, created_at)")
        }

        override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit
    }
}
