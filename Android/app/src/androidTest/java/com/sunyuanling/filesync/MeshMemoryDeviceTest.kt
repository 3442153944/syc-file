package com.sunyuanling.filesync

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.sunyuanling.filesync.mesh.RootMemoryIdentity
import com.wireguard.android.backend.Tunnel
import com.wireguard.android.backend.WgQuickBackend
import com.wireguard.android.util.RootShell
import com.wireguard.android.util.ToolsInstaller
import com.wireguard.config.Config
import com.wireguard.crypto.KeyPair
import java.io.ByteArrayInputStream
import java.io.File
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

/** Isolated kernel acceptance, independent of the test server's key hosting. */
@RunWith(AndroidJUnit4::class)
class MeshMemoryDeviceTest {
    @Test fun memoryKeyAndPeerRefreshKeepIdentity() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        require(context.packageName.endsWith(".meshtest"))
        val iface = "fs-key-test"
        val shell = RootShell(context)
        shell.start()
        val backend = WgQuickBackend(context, shell, ToolsInstaller(context, shell)).apply { setMultipleTunnels(true) }
        val tunnel = object : Tunnel {
            override fun getName() = iface
            override fun onStateChange(state: Tunnel.State) = Unit
        }
        fun read(field: String): String {
            val result = mutableListOf<String>()
            assertEquals(0, shell.run(result, "wg show $iface $field"))
            return result.joinToString("\n").trim()
        }
        val key = KeyPair()
        var up = false
        val publicPeers = File.createTempFile("memory-test-public-", ".conf", context.cacheDir)
        try {
            assertNotEquals("owned test interface already exists", 0, shell.run(mutableListOf(), "ip link show $iface"))
            val config = "[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 172.1.240.3/32\nListenPort = 51825\n"
            backend.setState(tunnel, Tunnel.State.UP, Config.parse(ByteArrayInputStream(config.toByteArray())))
            up = true
            RootMemoryIdentity.install(context, iface, 51825, key.privateKey.toBase64())
            assertEquals(key.publicKey.toBase64(), read("public-key"))
            publicPeers.writeText("[Interface]\n[Peer]\nPublicKey = CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAllowedIPs = 172.1.240.4/32\n")
            val path = publicPeers.absolutePath.replace("'", "'\\''")
            repeat(2) {
                assertEquals(0, shell.run(mutableListOf(), "wg addconf $iface '$path'"))
                assertEquals(key.publicKey.toBase64(), read("public-key"))
                assertEquals("51825", read("listen-port"))
            }
        } finally {
            publicPeers.delete()
            if (up) backend.setState(tunnel, Tunnel.State.DOWN, null)
            shell.stop()
        }
    }
}
