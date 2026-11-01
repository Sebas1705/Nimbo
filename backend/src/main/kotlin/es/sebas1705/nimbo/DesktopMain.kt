package es.sebas1705.nimbo

import java.awt.Desktop
import java.awt.MenuItem
import java.awt.PopupMenu
import java.awt.SystemTray
import java.awt.TrayIcon
import java.io.File
import java.io.FileOutputStream
import java.io.PrintStream
import java.net.URI
import java.util.concurrent.CountDownLatch
import kotlin.system.exitProcess

/**
 * Entry point of the Windows app (`Nimbo.exe`). It is the same server as the jar, wrapped so a double click is
 * enough: it starts Nimbo, opens the UI in the default browser and leaves an icon in the system tray whose menu
 * reopens the UI or quits. A second launch just reopens the UI of the one that is running.
 *
 * NIMBO_HEADLESS=1 skips the browser and the tray (used by tests and by anything scripted).
 */
object DesktopApp {
    @JvmStatic
    fun main(args: Array<String>) = launchDesktop()
}

internal fun launchDesktop() {
    val headless = System.getenv("NIMBO_HEADLESS") == "1"
    redirectOutputToLog()

    val config = NimboConfig.fromEnv()
    val plan = planLaunch(config.port, Probes::isNimbo, Probes::isFree, Probes::freePort)
    if (plan is LaunchPlan.OpenExisting) {
        if (!headless) openBrowser("http://127.0.0.1:${plan.port}")
        println("Nimbo already runs on port ${plan.port}; nothing to start.")
        exitProcess(0)
    }

    val port = (plan as LaunchPlan.Start).port
    val server = buildServer(config.copy(port = port, host = "127.0.0.1"))
    server.start(wait = false)
    val url = "http://127.0.0.1:$port"
    println("Nimbo ${config.version ?: "dev"} listening on $url")

    if (!headless) {
        openBrowser(url)
        addTrayIcon(url, config.version) {
            server.stop(300, 2000)
            exitProcess(0)
        }
    }
    // start(wait = false) returns at once, and nothing else is guaranteed to keep the JVM alive (the tray is
    // optional, headless runs have none): stay here until a Quit calls exitProcess.
    CountDownLatch(1).await()
}

/** A windowed app has no console; keep what it prints, since that is the only way to see why something failed. */
private fun redirectOutputToLog() {
    runCatching {
        val dir = appDataDir(System.getenv(), System.getProperty("user.home"))
        dir.mkdirs()
        val log = File(dir, "nimbo.log")
        rotateLog(log)
        val stream = PrintStream(FileOutputStream(log, true), true, Charsets.UTF_8)
        System.setOut(stream)
        System.setErr(stream)
    }
}

private fun openBrowser(url: String) {
    val opened = runCatching {
        if (Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.BROWSE)) {
            Desktop.getDesktop().browse(URI(url))
            true
        } else {
            false
        }
    }.getOrDefault(false)
    if (!opened) runCatching { ProcessBuilder("rundll32", "url.dll,FileProtocolHandler", url).start() }
}

private fun addTrayIcon(url: String, version: String?, onQuit: () -> Unit) {
    if (!SystemTray.isSupported()) {
        println("The system tray is not available: use the browser tab and close Nimbo from the task manager.")
        return
    }
    runCatching {
        val menu = PopupMenu()
        val open = MenuItem("Open Nimbo").apply { addActionListener { openBrowser(url) } }
        val quit = MenuItem("Quit Nimbo").apply { addActionListener { onQuit() } }
        menu.add(open)
        menu.addSeparator()
        menu.add(quit)
        val size = SystemTray.getSystemTray().trayIconSize.width.coerceAtLeast(16)
        val icon = TrayIcon(NimboIcon.image(size), "Nimbo ${version ?: ""} - $url".replace("  ", " "), menu).apply {
            isImageAutoSize = true
            addActionListener { openBrowser(url) } // double click
        }
        SystemTray.getSystemTray().add(icon)
        println("Tray icon added (${size}px).")
    }.onFailure { println("No tray icon: ${it.message}") }
}
