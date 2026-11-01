package es.sebas1705.nimbo

import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import java.nio.file.Files
import javax.imageio.ImageIO
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LaunchPlanTest {
    @Test
    fun `a running nimbo is reused instead of starting a second one`() {
        val plan = planLaunch(8080, isNimbo = { it == 8080 }, isFree = { error("must not probe") }, freePort = { error("must not pick") })
        assertEquals(LaunchPlan.OpenExisting(8080), plan)
    }

    @Test
    fun `a free preferred port is used`() {
        assertEquals(LaunchPlan.Start(8080), planLaunch(8080, isNimbo = { false }, isFree = { true }, freePort = { error("must not pick") }))
    }

    @Test
    fun `a port held by something else makes nimbo pick another one, not fail or take it over`() {
        val plan = planLaunch(8080, isNimbo = { false }, isFree = { false }, freePort = { 49999 })
        assertEquals(LaunchPlan.Start(49999), plan)
    }

    @Test
    fun `only nimbo health answers are recognised as nimbo`() {
        assertTrue(looksLikeNimbo("""{"status":"ok","authRequired":false,"version":"0.2.0"}"""))
        assertTrue(looksLikeNimbo("""{"status":"ok","authRequired":true,"version":null}"""))
        assertFalse(looksLikeNimbo("""{"status":"ok"}"""))
        assertFalse(looksLikeNimbo("""<html>Jenkins</html>"""))
        assertFalse(looksLikeNimbo(""))
    }

    @Test
    fun `the real port probes agree with the operating system`() {
        val held = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
        try {
            assertFalse(Probes.isFree(held.localPort), "a bound port is not free")
            assertFalse(Probes.isNimbo(held.localPort), "a socket that never answers is not nimbo")
        } finally {
            held.close()
        }
        val port = Probes.freePort()
        assertTrue(port in 1024..65535)
        assertTrue(Probes.isFree(port))
    }
}

class AppDataTest {
    @Test
    fun `windows keeps its data under localappdata, other systems under a hidden folder`() {
        assertEquals(File("C:/Users/a/AppData/Local", "Nimbo"), appDataDir(mapOf("LOCALAPPDATA" to "C:/Users/a/AppData/Local"), "C:/Users/a"))
        assertEquals(File("/home/a", ".nimbo"), appDataDir(emptyMap(), "/home/a"))
        assertEquals(File("/home/a", ".nimbo"), appDataDir(mapOf("LOCALAPPDATA" to "  "), "/home/a"))
    }

    @Test
    fun `a big log is moved aside once and a small one is left alone`() {
        val dir = Files.createTempDirectory("applog").toFile()
        try {
            val log = File(dir, "nimbo.log")
            log.writeText("x".repeat(100))
            rotateLog(log, maxBytes = 1000)
            assertTrue(log.exists() && !File(dir, "nimbo.log.1").exists())

            log.writeText("y".repeat(2000))
            rotateLog(log, maxBytes = 1000)
            assertFalse(log.exists())
            assertEquals(2000, File(dir, "nimbo.log.1").length())

            log.writeText("z".repeat(3000))
            rotateLog(log, maxBytes = 1000)
            assertEquals(3000, File(dir, "nimbo.log.1").length(), "only one previous generation is kept")
            rotateLog(File(dir, "missing.log"), 1) // a log that does not exist yet is fine
        } finally {
            dir.deleteRecursively()
        }
    }
}

class IconTest {
    @Test
    fun `the icon is drawn at any size with visible, coloured pixels`() {
        listOf(16, 32, 64, 256).forEach { size ->
            val img = NimboIcon.image(size)
            assertEquals(size, img.width)
            assertEquals(size, img.height)
            val centre = img.getRGB(size / 2, (size * 0.55).toInt())
            assertTrue((centre ushr 24) == 0xff, "the cloud is opaque at the centre ($size px)")
            assertTrue((centre and 0xffffff) != 0x0b0e17, "the centre shows the cloud, not the tile ($size px)")
        }
    }

    @Test
    fun `the ico has a valid header, one entry per size and png payloads`() {
        val sizes = listOf(16, 32, 256)
        val bytes = NimboIcon.ico(sizes)
        fun le16(at: Int) = (bytes[at].toInt() and 0xff) or ((bytes[at + 1].toInt() and 0xff) shl 8)
        fun le32(at: Int) = le16(at) or (le16(at + 2) shl 16)
        assertEquals(0, le16(0))
        assertEquals(1, le16(2), "type must be icon")
        assertEquals(3, le16(4))
        sizes.forEachIndexed { i, size ->
            val entry = 6 + 16 * i
            assertEquals(if (size >= 256) 0 else size, bytes[entry].toInt() and 0xff, "width of entry $i")
            val length = le32(entry + 8)
            val offset = le32(entry + 12)
            assertTrue(offset + length <= bytes.size)
            // every payload is a PNG
            assertEquals(listOf(0x89, 0x50, 0x4e, 0x47), (0..3).map { bytes[offset + it].toInt() and 0xff })
            val png = ImageIO.read(bytes.copyOfRange(offset, offset + length).inputStream())
            assertEquals(size, png.width)
        }
    }

    @Test
    fun `the committed icon is the one the code generates`() {
        val committed = listOf(File("packaging/nimbo.ico"), File("../packaging/nimbo.ico")).firstOrNull { it.isFile }
        assertTrue(committed != null, "packaging/nimbo.ico is missing: run ./gradlew :backend:generateIcon")
        // Compare decoded pixels, not bytes: a PNG encoder may differ between JDK builds without the picture changing.
        val bytes = committed.readBytes()
        fun le16(at: Int) = (bytes[at].toInt() and 0xff) or ((bytes[at + 1].toInt() and 0xff) shl 8)
        fun le32(at: Int) = le16(at) or (le16(at + 2) shl 16)
        val count = le16(4)
        assertTrue(count >= 3, "the icon needs several sizes")
        repeat(count) { i ->
            val entry = 6 + 16 * i
            val offset = le32(entry + 12)
            val decoded = ImageIO.read(bytes.copyOfRange(offset, offset + le32(entry + 8)).inputStream())
            val fresh = NimboIcon.image(decoded.width)
            val same = (0 until decoded.height).all { y -> (0 until decoded.width).all { x -> decoded.getRGB(x, y) == fresh.getRGB(x, y) } }
            assertTrue(same, "the ${decoded.width}px image differs from the code: run ./gradlew :backend:generateIcon and commit it")
        }
    }
}
