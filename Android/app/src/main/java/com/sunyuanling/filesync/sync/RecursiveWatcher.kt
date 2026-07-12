// sync/RecursiveWatcher.kt
// 职责：递归目录监听。Android FileObserver（inotify）只看单层目录，
// 这里为根下每个子目录各挂一个 observer，目录新增/移入时动态补挂。
//
// 注意：FileObserver 实例必须被强引用（GC 回收即停止监听），统一收在 observers 里。
package com.sunyuanling.filesync.sync

import android.os.FileObserver
import java.io.File
import java.util.concurrent.ConcurrentHashMap

class RecursiveWatcher(
    private val root: File,
    /** 目录名过滤：返回 true 的目录不监听也不深入（.synctmp/.syncpending 等） */
    private val skipDir: (name: String) -> Boolean,
    /** 事件回调（FileObserver 线程上调用，调用方自行切协程） */
    private val onEvent: (file: File, event: Int) -> Unit,
) {

    companion object {
        const val MASK = FileObserver.CREATE or FileObserver.CLOSE_WRITE or
                FileObserver.MOVED_TO or FileObserver.MOVED_FROM or
                FileObserver.DELETE or FileObserver.DELETE_SELF or FileObserver.MOVE_SELF
    }

    private val observers = ConcurrentHashMap<String, FileObserver>()

    fun start() {
        addWatchRecursive(root)
    }

    fun stop() {
        observers.values.forEach { it.stopWatching() }
        observers.clear()
    }

    private fun addWatchRecursive(dir: File) {
        if (!dir.isDirectory || skipDir(dir.name)) return
        watch(dir)
        dir.listFiles()?.forEach { child ->
            if (child.isDirectory) addWatchRecursive(child)
        }
    }

    private fun watch(dir: File) {
        val key = dir.absolutePath
        if (observers.containsKey(key)) return
        @Suppress("DEPRECATION") // File 构造器要 API 29，本项目 minSdk 24
        val obs = object : FileObserver(key, MASK) {
            override fun onEvent(event: Int, path: String?) {
                val e = event and ALL_EVENTS
                if (path == null) {
                    // 目录自身被删/被移：摘掉该 observer
                    if (e and (DELETE_SELF or MOVE_SELF) != 0) {
                        observers.remove(key)?.stopWatching()
                    }
                    return
                }
                val f = File(key, path)
                // 新目录（创建或移入）：动态补挂递归监听
                if (e and (CREATE or MOVED_TO) != 0 && f.isDirectory) {
                    addWatchRecursive(f)
                }
                onEvent(f, e)
            }
        }
        obs.startWatching()
        observers[key] = obs
    }
}
