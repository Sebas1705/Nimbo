package es.sebas1705.nimbo

import java.awt.BasicStroke
import java.awt.Color
import java.awt.GradientPaint
import java.awt.RenderingHints
import java.awt.geom.Area
import java.awt.geom.Ellipse2D
import java.awt.geom.RoundRectangle2D
import java.awt.image.BufferedImage
import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.ServerSocket
import java.net.URI
import javax.imageio.ImageIO

/** What the desktop launcher does at start-up. */
sealed interface LaunchPlan {
    /** Another Nimbo already answers on this port: just show it. */
    data class OpenExisting(val port: Int) : LaunchPlan

    data class Start(val port: Int) : LaunchPlan
}

/**
 * Decides between reusing a running Nimbo, taking the preferred port, or falling back to a free one.
 * Pure on purpose: the probes are passed in, so every branch is testable without sockets.
 */
fun planLaunch(preferred: Int, isNimbo: (Int) -> Boolean, isFree: (Int) -> Boolean, freePort: () -> Int): LaunchPlan = when {
    isNimbo(preferred) -> LaunchPlan.OpenExisting(preferred)
    isFree(preferred) -> LaunchPlan.Start(preferred)
    else -> LaunchPlan.Start(freePort())
}

/** True when a /api/health body is Nimbo's: it is the only thing that answers with these two fields. */
fun looksLikeNimbo(body: String): Boolean = "\"status\":\"ok\"" in body && "\"authRequired\"" in body

/** Where the desktop app keeps its log: %LOCALAPPDATA%\Nimbo on Windows, ~/.nimbo elsewhere. */
fun appDataDir(env: Map<String, String>, home: String): File =
    env["LOCALAPPDATA"]?.takeIf { it.isNotBlank() }?.let { File(it, "Nimbo") } ?: File(home, ".nimbo")

/** Moves a log that grew past [maxBytes] aside, keeping a single previous generation. */
fun rotateLog(file: File, maxBytes: Long = 2L * 1024 * 1024) {
    if (!file.isFile || file.length() <= maxBytes) return
    val old = File(file.parentFile, file.name + ".1")
    old.delete()
    file.renameTo(old)
}

object Probes {
    fun isNimbo(port: Int): Boolean = try {
        val c = URI("http://127.0.0.1:$port/api/health").toURL().openConnection() as HttpURLConnection
        c.connectTimeout = 700
        c.readTimeout = 700
        c.inputStream.use { looksLikeNimbo(it.readBytes().decodeToString()) }
    } catch (e: Exception) {
        false
    }

    fun isFree(port: Int): Boolean = try {
        ServerSocket(port, 1, InetAddress.getByName("127.0.0.1")).use { true }
    } catch (e: Exception) {
        false
    }

    fun freePort(): Int = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
}

/** The Nimbo cloud, drawn in code so no binary asset has to be kept in sync. */
object NimboIcon {
    fun image(size: Int): BufferedImage {
        val img = BufferedImage(size, size, BufferedImage.TYPE_INT_ARGB)
        val g = img.createGraphics()
        try {
            g.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            g.setRenderingHint(RenderingHints.KEY_STROKE_CONTROL, RenderingHints.VALUE_STROKE_PURE)
            val s = size / 32.0
            // A rounded dark tile keeps the cloud readable on light and dark taskbars.
            g.color = Color(0x0b, 0x0e, 0x17)
            g.fill(RoundRectangle2D.Double(0.0, 0.0, size.toDouble(), size.toDouble(), 7 * s, 7 * s))
            val cloud = Area(Ellipse2D.Double(5.5 * s, 12.5 * s, 11 * s, 11 * s))
            cloud.add(Area(Ellipse2D.Double(10.0 * s, 6.0 * s, 13 * s, 13 * s)))
            cloud.add(Area(Ellipse2D.Double(16.0 * s, 11.5 * s, 11 * s, 11 * s)))
            cloud.add(Area(RoundRectangle2D.Double(9.0 * s, 15.0 * s, 13.5 * s, 8.5 * s, 2 * s, 2 * s)))
            g.paint = GradientPaint(5f * s.toFloat(), 6f * s.toFloat(), Color(0x9d, 0xb4, 0xff), 27f * s.toFloat(), 24f * s.toFloat(), Color(0x6e, 0xe7, 0xc8))
            g.fill(cloud)
            g.stroke = BasicStroke(0f)
        } finally {
            g.dispose()
        }
        return img
    }

    /** An .ico holding PNG-compressed images, which Windows has understood since Vista. */
    fun ico(sizes: List<Int> = listOf(16, 24, 32, 48, 64, 128, 256)): ByteArray {
        val pngs = sizes.map { size ->
            ByteArrayOutputStream().also { ImageIO.write(image(size), "png", it) }.toByteArray()
        }
        val out = ByteArrayOutputStream()
        DataOutputStream(out).use { d ->
            fun le16(v: Int) = d.write(byteArrayOf((v and 0xff).toByte(), ((v shr 8) and 0xff).toByte()))
            fun le32(v: Int) = d.write(byteArrayOf((v and 0xff).toByte(), ((v shr 8) and 0xff).toByte(), ((v shr 16) and 0xff).toByte(), ((v shr 24) and 0xff).toByte()))
            le16(0) // reserved
            le16(1) // type: icon
            le16(sizes.size)
            var offset = 6 + 16 * sizes.size
            sizes.forEachIndexed { i, size ->
                d.write(if (size >= 256) 0 else size) // width, 0 means 256
                d.write(if (size >= 256) 0 else size) // height
                d.write(0) // palette
                d.write(0) // reserved
                le16(1) // colour planes
                le16(32) // bits per pixel
                le32(pngs[i].size)
                le32(offset)
                offset += pngs[i].size
            }
            pngs.forEach { d.write(it) }
        }
        return out.toByteArray()
    }
}

/** `gradle generateIcon` runs this to refresh packaging/nimbo.ico. */
object IconTool {
    @JvmStatic
    fun main(args: Array<String>) {
        val target = File(args.firstOrNull() ?: "packaging/nimbo.ico")
        target.parentFile?.mkdirs()
        target.writeBytes(NimboIcon.ico())
        println("wrote ${target.path} (${target.length()} bytes)")
    }
}
