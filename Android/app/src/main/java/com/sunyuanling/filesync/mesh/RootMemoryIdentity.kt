package com.sunyuanling.filesync.mesh

import android.content.Context
import java.io.File
import java.util.concurrent.TimeUnit

/** Passes the authenticated key to wg stdin, never a shell argument or file. */
internal object RootMemoryIdentity {
    fun install(context: Context, interfaceName: String, listenPort: Int, key: String) {
        require(interfaceName.matches(Regex("[A-Za-z0-9_-]{1,15}")) && listenPort in 1..65535)
        require(key.matches(Regex("[A-Za-z0-9+/]{43}=")))
        val wg = File(context.codeCacheDir, "bin/wg")
        check(wg.isFile) { "组网工具未安装" }
        val path = wg.absolutePath.replace("'", "'\\''")
        val process = ProcessBuilder("su", "-c", "'$path' set $interfaceName private-key /dev/stdin listen-port $listenPort")
            .redirectErrorStream(true).start()
        try {
            process.outputStream.use { it.write((key + "\n").toByteArray(Charsets.US_ASCII)) }
            check(process.waitFor(10, TimeUnit.SECONDS) && process.exitValue() == 0) { "内存组网身份配置失败" }
        } finally {
            process.inputStream.close()
            process.destroy()
        }
    }
}
