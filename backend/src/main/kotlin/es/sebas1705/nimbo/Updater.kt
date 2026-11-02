package es.sebas1705.nimbo

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.io.File
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.security.MessageDigest
import java.time.Duration

/** `1.2.3` or `1.2.3-beta.1`, with an optional leading `v`. A release without a suffix is newer than the same one with it. */
data class SemVer(val major: Int, val minor: Int, val patch: Int, val pre: String? = null) : Comparable<SemVer> {
    override fun compareTo(other: SemVer): Int {
        compareValuesBy(this, other, { it.major }, { it.minor }, { it.patch }).let { if (it != 0) return it }
        if (pre == other.pre) return 0
        if (pre == null) return 1
        if (other.pre == null) return -1
        val a = pre.split('.')
        val b = other.pre.split('.')
        for (i in 0 until maxOf(a.size, b.size)) {
            val x = a.getOrNull(i) ?: return -1
            val y = b.getOrNull(i) ?: return 1
            val xi = x.toIntOrNull()
            val yi = y.toIntOrNull()
            val c = when {
                xi != null && yi != null -> xi.compareTo(yi)
                xi != null -> -1
                yi != null -> 1
                else -> x.compareTo(y)
            }
            if (c != 0) return c
        }
        return 0
    }

    override fun toString() = "$major.$minor.$patch" + (pre?.let { "-$it" } ?: "")

    companion object {
        private val RE = Regex("v?(\\d{1,5})\\.(\\d{1,5})\\.(\\d{1,5})(?:-([0-9A-Za-z.-]{1,32}))?")
        fun parse(text: String?): SemVer? {
            val m = RE.matchEntire(text?.trim().orEmpty()) ?: return null
            val (a, b, c, pre) = m.destructured
            return SemVer(a.toInt(), b.toInt(), c.toInt(), pre.ifEmpty { null })
        }
    }
}

@Serializable data class UpdateAsset(val name: String, val size: Long, val url: String)
@Serializable data class UpdateRelease(val tag: String, val version: String, val notes: String, val publishedAt: String?, val assets: List<UpdateAsset>)

/** A file on disk that Nimbo could install. [verified] is true only when its SHA-256 matched a published checksum. */
@Serializable data class UpdateFile(val version: String, val name: String, val sha256: String, val verified: Boolean, val source: String, val size: Long, val at: Long)

@Serializable
data class UpdateStatus(
    val current: String?,
    val distribution: String,
    val mode: String,
    val checkedAt: Long?,
    val latest: UpdateRelease?,
    val available: Boolean,
    /** The file of the latest release that fits how Nimbo was installed; null when there is none (Docker updates by pulling). */
    val asset: UpdateAsset?,
    val files: List<UpdateFile>,
    val canInstall: Boolean,
    val error: String? = null,
    /** What to do by hand when Nimbo cannot update itself. */
    val manual: String? = null,
)

@Serializable data class DownloadRequestDto(val version: String)
@Serializable data class ImportUpdateDto(val path: String, val sha256: String? = null)
@Serializable data class InstallRequestDto(val version: String, val confirm: Boolean = false)

/** Reads from the network. Faked in tests. */
interface Downloader {
    suspend fun text(url: String): String
    suspend fun file(url: String, target: File, maxBytes: Long)
}

class JavaDownloader : Downloader {
    private val client = HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1)
        .followRedirects(HttpClient.Redirect.NORMAL).connectTimeout(Duration.ofSeconds(10)).build()

    private fun request(url: String) = HttpRequest.newBuilder(URI.create(url)).timeout(Duration.ofMinutes(15))
        .header("User-Agent", "Nimbo-updater").header("Accept", "application/vnd.github+json, application/octet-stream, */*").build()

    override suspend fun text(url: String): String = withContext(Dispatchers.IO) {
        val r = client.send(request(url), HttpResponse.BodyHandlers.ofString())
        if (r.statusCode() != 200) throw UpdateError("GitHub answered ${r.statusCode()} for ${URI.create(url).path}")
        r.body()
    }

    override suspend fun file(url: String, target: File, maxBytes: Long): Unit = withContext(Dispatchers.IO) {
        val r = client.send(request(url), HttpResponse.BodyHandlers.ofInputStream())
        if (r.statusCode() != 200) throw UpdateError("The download answered ${r.statusCode()}")
        r.body().use { input ->
            target.outputStream().use { out ->
                val buf = ByteArray(64 * 1024)
                var total = 0L
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    total += n
                    if (total > maxBytes) throw UpdateError("The download is bigger than expected")
                    out.write(buf, 0, n)
                }
            }
        }
    }
}

class UpdateError(message: String) : RuntimeException(message)

/** Hands an installer to Windows and leaves. Faked in tests: nothing in the test suite ever runs an installer. */
fun interface Installer {
    fun launch(msi: File, nimboPid: Long, exe: String?)
}

class WindowsInstaller(private val scriptDir: File) : Installer {
    override fun launch(msi: File, nimboPid: Long, exe: String?) {
        val script = writeScript()
        ProcessBuilder(
            "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass",
            "-File", script.path, "-Msi", msi.path, "-NimboPid", nimboPid.toString(), "-Exe", exe.orEmpty(),
        ).start()
    }

    fun writeScript(): File {
        scriptDir.mkdirs()
        val script = File(scriptDir, "install-update.ps1")
        // Paths come in as parameters, never pasted into the script, so a strange file name cannot become a command.
        script.writeText(
            """
            param([string]${'$'}Msi, [int]${'$'}NimboPid, [string]${'$'}Exe)
            ${'$'}ErrorActionPreference = 'Continue'
            try { Wait-Process -Id ${'$'}NimboPid -Timeout 60 -ErrorAction Stop } catch {}
            Start-Process -FilePath msiexec.exe -ArgumentList @('/i', ${'$'}Msi, '/passive', '/norestart') -Wait
            if (${'$'}Exe -and (Test-Path -LiteralPath ${'$'}Exe)) { Start-Process -FilePath ${'$'}Exe }
            """.trimIndent(),
        )
        return script
    }
}

/**
 * Looks for new versions, downloads them checking their SHA-256 against the release's SHA256SUMS, and — only when you
 * confirm — hands the installer over. It never contacts anyone in manual mode, and only ever talks to this repository.
 */
class Updater(
    private val config: NimboConfig,
    private val data: AppData,
    private val downloader: Downloader,
    private val folder: File,
    private val installer: Installer = Installer { _, _, _ -> throw UpdateError("This installation cannot update itself") },
    private val clock: () -> Long = System::currentTimeMillis,
    private val currentPid: () -> Long = { ProcessHandle.current().pid() },
    private val currentExe: () -> String? = { ProcessHandle.current().info().command().orElse(null) },
    /** Called after the installer has been launched so Nimbo can quit and let Windows replace it. */
    private val quit: () -> Unit = {},
    private val repo: String = "Sebas1705/Nimbo",
) {
    private val lock = Mutex()
    @Volatile private var latest: UpdateRelease? = null
    @Volatile private var lastError: String? = null

    private val releasesUrl get() = "https://api.github.com/repos/$repo/releases?per_page=10"
    private val downloadPrefix get() = "https://github.com/$repo/releases/download/"
    private val current: SemVer? get() = SemVer.parse(config.version)
    private val updatesDir get() = File(folder, "updates")
    private val registry = "updates.json"

    // -- status ---------------------------------------------------------------------------------

    fun status(): UpdateStatus {
        val release = latest
        val newer = release != null && SemVer.parse(release.version)?.let { v -> current?.let { v > it } ?: true } == true
        val asset = release?.let { pickAsset(it) }
        val settings = data.settings.get()
        return UpdateStatus(
            current = config.version, distribution = config.distribution, mode = settings.updateMode, checkedAt = settings.lastUpdateCheck,
            latest = release, available = newer, asset = asset.takeIf { newer }, files = files(),
            canInstall = config.distribution == "desktop" && System.getProperty("os.name").orEmpty().startsWith("Windows"),
            error = lastError, manual = manualSteps(release?.version, newer),
        )
    }

    private fun manualSteps(version: String?, newer: Boolean): String? = when {
        !newer -> null
        config.distribution == "docker" -> "docker compose pull && docker compose up -d"
        config.distribution == "jar" -> "Replace your nimbo.jar with nimbo-$version.jar from the downloaded file and start it again."
        else -> null
    }

    // -- checking -------------------------------------------------------------------------------

    /** Asks GitHub for the newest release. With [force] false it respects the update mode and asks at most once a day. */
    suspend fun check(force: Boolean): UpdateStatus = lock.withLock {
        val settings = data.settings.get()
        val due = settings.lastUpdateCheck?.let { clock() - it >= DAY_MS } ?: true
        if (!force && (settings.updateMode == "manual" || !due)) return status()
        try {
            val text = downloader.text(releasesUrl)
            latest = newest(text)
            lastError = if (latest == null) "No published versions were found" else null
        } catch (e: Exception) {
            lastError = if (e is UpdateError) e.message else "Could not reach GitHub: ${e.message ?: e.javaClass.simpleName}"
        }
        data.settings.markUpdateCheck(clock())
        data.stats.record("update.check", "github", lastError ?: latest?.version)
        status()
    }

    private fun newest(text: String): UpdateRelease? {
        val array = Json.parseToJsonElement(text) as? JsonArray ?: return null
        return array.mapNotNull { it as? JsonObject }
            .filter { it["draft"]?.jsonPrimitive?.booleanOrNull != true }
            .mapNotNull { o ->
                val tag = o["tag_name"]?.jsonPrimitive?.content ?: return@mapNotNull null
                val v = SemVer.parse(tag) ?: return@mapNotNull null
                val assets = o["assets"]?.jsonArray.orEmpty().mapNotNull { a ->
                    val ao = a.jsonObject
                    val url = ao["browser_download_url"]?.jsonPrimitive?.content ?: return@mapNotNull null
                    val name = ao["name"]?.jsonPrimitive?.content ?: return@mapNotNull null
                    if (!url.startsWith(downloadPrefix)) return@mapNotNull null
                    UpdateAsset(name, ao["size"]?.jsonPrimitive?.longOrNull ?: 0, url)
                }
                v to UpdateRelease(tag, v.toString(), o["body"]?.jsonPrimitive?.content.orEmpty().take(4000), o["published_at"]?.jsonPrimitive?.content, assets)
            }
            .maxByOrNull { it.first }?.second
    }

    private fun pickAsset(release: UpdateRelease): UpdateAsset? {
        val v = release.version
        val wanted = when (config.distribution) {
            "desktop" -> "Nimbo-$v-windows-x64.msi"
            "jar" -> "nimbo-$v.jar"
            else -> return null
        }
        return release.assets.firstOrNull { it.name == wanted }
    }

    // -- downloading ----------------------------------------------------------------------------

    /** Downloads the asset for this installation and keeps it only if its SHA-256 matches the release's SHA256SUMS. */
    suspend fun download(version: String): UpdateFile = lock.withLock {
        val release = latest?.takeIf { it.version == version } ?: throw InvalidParam("Check for updates first: $version is not the latest known version")
        val asset = pickAsset(release) ?: throw InvalidParam("There is nothing to download for this installation (${config.distribution})")
        if (!asset.url.startsWith(downloadPrefix)) throw UpdateError("Refusing to download from outside $repo")
        val sums = release.assets.firstOrNull { it.name == "SHA256SUMS" } ?: throw UpdateError("This release publishes no SHA256SUMS, so it cannot be verified")
        val expected = parseSums(downloader.text(sums.url))[asset.name] ?: throw UpdateError("SHA256SUMS does not list ${asset.name}")

        updatesDir.mkdirs()
        val part = File(updatesDir, "${asset.name}.part")
        try {
            downloader.file(asset.url, part, maxOf(asset.size, 1) + 1024 * 1024)
            val actual = sha256(part)
            if (!actual.equals(expected, ignoreCase = true)) {
                throw UpdateError("The downloaded file does not match its published checksum, so it was deleted")
            }
            val target = File(updatesDir, asset.name)
            part.copyTo(target, overwrite = true)
            register(UpdateFile(release.version, asset.name, actual, verified = true, source = "github", size = target.length(), at = clock()))
                .also { data.stats.record("update.download", release.version) }
        } finally {
            part.delete()
        }
    }

    // -- importing a file (offline) -------------------------------------------------------------

    /**
     * Takes an installer you brought yourself (from the offline kit, a USB stick). When a SHA256SUMS sits next to it, or you
     * give the expected hash, the file is checked; otherwise it is accepted but marked as not verified.
     */
    suspend fun import(path: String, expected: String?): UpdateFile = lock.withLock {
        val source = File(path)
        if (!source.isFile) throw InvalidParam("No file at that path")
        val version = versionOf(source.name) ?: throw InvalidParam("That does not look like a Nimbo installer (Nimbo-<version>-windows-x64.msi or nimbo-<version>.jar)")
        val actual = sha256(source)
        val given = expected?.trim()?.lowercase()?.takeIf { it.isNotEmpty() }
        if (given != null && !Regex("[0-9a-f]{64}").matches(given)) throw InvalidParam("The checksum must be 64 hexadecimal characters")
        val beside = File(source.parentFile, "SHA256SUMS").takeIf { it.isFile }?.let { parseSums(it.readText())[source.name] }
        val reference = given ?: beside?.lowercase()
        if (reference != null && reference != actual) throw UpdateError("The file does not match the checksum, so it was not imported")
        updatesDir.mkdirs()
        val target = File(updatesDir, source.name)
        if (source.canonicalFile != target.canonicalFile) source.copyTo(target, overwrite = true)
        register(UpdateFile(version.toString(), source.name, actual, verified = reference != null, source = "file", size = target.length(), at = clock()))
            .also { data.stats.record("update.import", version.toString(), if (reference != null) "verified" else "not verified") }
    }

    // -- installing -----------------------------------------------------------------------------

    /** Re-checks the file against the hash recorded when it was accepted, then hands it to Windows. Only for the desktop app. */
    suspend fun install(version: String, confirm: Boolean) = lock.withLock {
        if (!confirm) throw InvalidParam("Installing replaces the running app: ask again with confirm=true")
        if (config.distribution != "desktop") throw InvalidParam("Only the Windows app installs updates by itself")
        val entry = files().firstOrNull { it.version == version && it.name.endsWith(".msi") } ?: throw UnknownTarget("No downloaded installer for $version")
        val file = File(updatesDir, entry.name)
        if (!file.isFile || !sha256(file).equals(entry.sha256, ignoreCase = true)) throw UpdateError("The installer changed on disk since it was accepted; download it again")
        val target = SemVer.parse(version) ?: throw InvalidParam("Bad version")
        current?.let { if (target <= it) throw InvalidParam("$version is not newer than the installed ${it}") }
        installer.launch(file, currentPid(), currentExe())
        data.stats.record("update.install", version, if (entry.verified) "verified" else "not verified")
        quit()
    }

    // -- bookkeeping ----------------------------------------------------------------------------

    fun files(): List<UpdateFile> =
        data.store.readOrDefault(registry) { emptyList<UpdateFile>() }.filter { File(updatesDir, it.name).isFile }
            .sortedByDescending { SemVer.parse(it.version) ?: SemVer(0, 0, 0) }

    /** Deletes downloaded installers (all of them, or the ones older than the running version). */
    @Synchronized
    fun clean(): Int {
        val all = files()
        all.forEach { File(updatesDir, it.name).delete() }
        data.store.writeJson(registry, emptyList<UpdateFile>())
        return all.size
    }

    @Synchronized
    private fun register(file: UpdateFile): UpdateFile {
        val rest = data.store.readOrDefault(registry) { emptyList<UpdateFile>() }.filter { it.name != file.name }
        data.store.write(registry, dataJson.encodeToString(ListSerializer(UpdateFile.serializer()), rest + file))
        return file
    }

    companion object {
        const val DAY_MS = 24 * 60 * 60 * 1000L
        private val NAME = Regex("(?:Nimbo-(.+)-windows-x64\\.msi|nimbo-(.+)\\.jar)")

        fun versionOf(fileName: String): SemVer? = NAME.matchEntire(fileName)?.let { m -> SemVer.parse(m.groupValues[1].ifEmpty { m.groupValues[2] }) }

        /** `<hash>  <name>` per line, as written by sha256sum. */
        fun parseSums(text: String): Map<String, String> = text.lineSequence().mapNotNull { line ->
            val m = Regex("([0-9a-fA-F]{64})\\s+\\*?(\\S+)").matchEntire(line.trim()) ?: return@mapNotNull null
            m.groupValues[2] to m.groupValues[1].lowercase()
        }.toMap()

        fun sha256(file: File): String {
            val md = MessageDigest.getInstance("SHA-256")
            file.inputStream().use { input ->
                val buf = ByteArray(64 * 1024)
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    md.update(buf, 0, n)
                }
            }
            return md.digest().joinToString("") { "%02x".format(it) }
        }
    }
}
