package com.sunyuanling.filesync

import com.sunyuanling.filesync.network.ServerAddress
import org.junit.Assert.*
import org.junit.Test

class ServerAddressTest {
    @Test fun normalizesProxyPrefixAndSupportsDirectBackend() {
        assertEquals("http://192.168.31.100:80/file", ServerAddress.base(" 192.168.31.100 ", 80, false, "file/"))
        assertEquals("http://192.168.31.100:8891", ServerAddress.base("192.168.31.100", 8891, false, "/"))
        assertEquals("https://[::1]:443/file", ServerAddress.base("::1", 443, true, "/file/"))
    }

    @Test fun notFoundAndUnrelatedWebpagesAreNotSuccessfulHealthChecks() {
        val healthy = """{"code":200,"data":{"status":"ok"},"message":"pong"}"""
        assertTrue(ServerAddress.healthy(200, healthy))
        assertFalse(ServerAddress.healthy(404, healthy))
        assertFalse(ServerAddress.healthy(200, "<html>Welcome</html>"))
        assertFalse(ServerAddress.healthy(200, """{"code":401,"message":"unauthorized"}"""))
    }
}
