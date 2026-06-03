package com.example.filesync.util

import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import java.io.BufferedWriter
import java.io.File
import java.io.FileWriter
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

object FileLogger {

    private const val INNER_TAG = "FileLogger"

    private data class Entry(
        val time: Long,
        val level: LogLevel,
        val tag: String,
        val message: String,
        val throwable: Throwable?,
    )

    @Volatile private var config: FileLoggerConfig? = null
    private var channel: Channel<Entry>? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val dateFormat = SimpleDateFormat("MM-dd HH:mm:ss.SSS", Locale.getDefault())

    // 仅消费协程访问，无需加锁
    private var writer: BufferedWriter? = null
    private var bytesWritten: Long = 0

    fun init(cfg: FileLoggerConfig) {
        if (config != null) return
        cfg.logDir.mkdirs()
        config = cfg
        val ch = Channel<Entry>(
            capacity = 4096,
            onBufferOverflow = BufferOverflow.DROP_OLDEST, // 队列爆了丢最老的，绝不阻塞调用方
        )
        channel = ch
        scope.launch { consume(cfg, ch) }
    }

    // ---------- 对外 API（签名贴近 android.util.Log，方便迁移） ----------
    fun v(tag: String, msg: String) = log(LogLevel.VERBOSE, tag, msg, null)
    fun d(tag: String, msg: String) = log(LogLevel.DEBUG, tag, msg, null)
    fun i(tag: String, msg: String) = log(LogLevel.INFO, tag, msg, null)
    fun w(tag: String, msg: String, tr: Throwable? = null) = log(LogLevel.WARN, tag, msg, tr)
    fun e(tag: String, msg: String, tr: Throwable? = null) = log(LogLevel.ERROR, tag, msg, tr)

    private fun log(level: LogLevel, tag: String, msg: String, tr: Throwable?) {
        val cfg = config ?: return
        if (level.priority < cfg.minLevel.priority) return

        if (cfg.alsoLogcat) {
            when (level) {
                LogLevel.VERBOSE -> Log.v(tag, msg, tr)
                LogLevel.DEBUG   -> Log.d(tag, msg, tr)
                LogLevel.INFO    -> Log.i(tag, msg, tr)
                LogLevel.WARN    -> Log.w(tag, msg, tr)
                LogLevel.ERROR   -> Log.e(tag, msg, tr)
            }
        }
        channel?.trySend(Entry(System.currentTimeMillis(), level, tag, msg, tr))
    }

    // ---------- 后台消费 ----------
    private suspend fun consume(cfg: FileLoggerConfig, ch: Channel<Entry>) {
        for (first in ch) {
            try {
                writeEntry(cfg, first)
                // 把当前堆积的全部取出来批量写
                while (true) {
                    val next = ch.tryReceive().getOrNull() ?: break
                    writeEntry(cfg, next)
                }
                writer?.flush()
            } catch (e: Exception) {
                Log.e(INNER_TAG, "写日志失败", e)
            }
        }
        // channel 被关闭后收尾
        runCatching { writer?.flush(); writer?.close() }
        writer = null
    }

    private fun writeEntry(cfg: FileLoggerConfig, e: Entry) {
        ensureWriter(cfg)
        val line = buildString {
            append(dateFormat.format(Date(e.time)))
            append(' ').append(e.level.flag)
            append('/').append(e.tag)
            append(": ").append(e.message)
            e.throwable?.let { append('\n').append(Log.getStackTraceString(it)) }
            append('\n')
        }
        writer!!.write(line)
        bytesWritten += line.toByteArray(Charsets.UTF_8).size
        if (bytesWritten >= cfg.maxFileSize) rotate(cfg)
    }

    private fun ensureWriter(cfg: FileLoggerConfig) {
        if (writer != null) return
        val current = fileForIndex(cfg, 0)
        bytesWritten = current.length()
        writer = BufferedWriter(FileWriter(current, /* append = */ true))
    }

    private fun rotate(cfg: FileLoggerConfig) {
        writer?.flush()
        writer?.close()
        writer = null

        // 删除最老的，其余依次后移：app.4.log 删除，app.3→app.4 ... app.log→app.1.log
        fileForIndex(cfg, cfg.maxFileCount - 1).takeIf { it.exists() }?.delete()
        for (i in cfg.maxFileCount - 2 downTo 0) {
            val src = fileForIndex(cfg, i)
            if (src.exists()) src.renameTo(fileForIndex(cfg, i + 1))
        }
        bytesWritten = 0
        // 下次 writeEntry 会重新 ensureWriter，创建新的 app.log
    }

    /** index 0 = app.log，index n = app.n.log */
    private fun fileForIndex(cfg: FileLoggerConfig, index: Int): File {
        if (index == 0) return File(cfg.logDir, cfg.fileName)
        val dot = cfg.fileName.lastIndexOf('.')
        val name = if (dot > 0) {
            cfg.fileName.substring(0, dot) + ".$index" + cfg.fileName.substring(dot)
        } else {
            "${cfg.fileName}.$index"
        }
        return File(cfg.logDir, name)
    }

    /** 进程退出/崩溃前调用，阻塞等待落盘 */
    fun flushBlocking() = runBlocking {
        runCatching { writer?.flush() }
    }
}