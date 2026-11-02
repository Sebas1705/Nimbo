package es.sebas1705.nimbo

import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.server.testing.testApplication
import kotlinx.coroutines.runBlocking
import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val BASE = "https://github.com/Sebas1705/Nimbo/releases/download"
private const val MSI_BYTES = "pretend this is an installer"

private fun sha(text: String): String {
    val f = Files.createTempFile("sha", ".bin").toFile().apply { writeText(text) }
    return Updater.sha256(f).also { f.delete() }
}

private fun releasesJson(vararg versions: String, sums: (String) -> String = { v -> "${sha(MSI_BYTES)}  Nimbo-$v-windows-x64.msi\n" }) =
    versions.joinToString(prefix = "[", postfix = "]") { v ->
        """{"tag_name":"v$v","draft":false,"prerelease":true,"body":"notes $v","published_at":"2026-11-01T00:00:00Z","assets":[
            {"name":"Nimbo-$v-windows-x64.msi","size":${MSI_BYTES.length},"browser_download_url":"$BASE/v$v/Nimbo-$v-windows-x64.msi"},
            {"name":"nimbo-$v.jar","size":10,"browser_download_url":"$BASE/v$v/nimbo-$v.jar"},
            {"name":"SHA256SUMS","size":100,"browser_download_url":"$BASE/v$v/SHA256SUMS"},
            {"name":"evil.exe","size":1,"browser_download_url":"https://evil.example/evil.exe"}]}"""
    }

private class FakeDownloader(var releases: String, val files: MutableMap<String, String> = mutableMapOf(), var fail: String? = null) : Downloader {
    val asked = mutableListOf<String>()
    override suspend fun text(url: String): String {
        asked += url
        fail?.let { throw UpdateError(it) }
        if (url.contains("/releases?")) return releases
        if (url.endsWith("/SHA256SUMS")) {
            val v = url.substringAfter("/download/v").substringBefore('/')
            return files["SHA256SUMS-$v"] ?: "${sha(MSI_BYTES)}  Nimbo-$v-windows-x64.msi\n"
        }
        throw UpdateError("unexpected $url")
    }
    override suspend fun file(url: String, target: File, maxBytes: Long) {
        asked += url
        target.writeText(files[url.substringAfterLast('/')] ?: MSI_BYTES)
    }
}

private class Launches : Installer {
    val calls = mutableListOf<File>()
    override fun launch(msi: File, nimboPid: Long, exe: String?) { calls += msi }
}

private fun folder() = Files.createTempDirectory("nimbo-upd").toFile().also { it.deleteOnExit() }

class SemVerTest {
    @Test
    fun `parses and orders versions`() {
        assertEquals(SemVer(0, 2, 1), SemVer.parse("v0.2.1"))
        assertEquals(SemVer(1, 0, 0, "beta.2"), SemVer.parse("1.0.0-beta.2"))
        assertNull(SemVer.parse("latest"))
        assertNull(SemVer.parse("1.2"))
        assertNull(SemVer.parse(null))
        val order = listOf("0.2.1", "0.10.0", "0.3.0", "1.0.0-beta.2", "1.0.0-beta.10", "1.0.0", "1.0.0-alpha").map { SemVer.parse(it)!! }.sorted().map { it.toString() }
        assertEquals(listOf("0.2.1", "0.3.0", "0.10.0", "1.0.0-alpha", "1.0.0-beta.2", "1.0.0-beta.10", "1.0.0"), order)
    }

    @Test
    fun `installer names carry the version`() {
        assertEquals(SemVer(0, 3, 0), Updater.versionOf("Nimbo-0.3.0-windows-x64.msi"))
        assertEquals(SemVer(0, 3, 0), Updater.versionOf("nimbo-0.3.0.jar"))
        assertNull(Updater.versionOf("evil.exe"))
        assertNull(Updater.versionOf("Nimbo-0.3.0-windows-x64.msi.exe"))
    }

    @Test
    fun `checksum files are parsed`() {
        val h = "a".repeat(64)
        assertEquals(mapOf("x.msi" to h), Updater.parseSums("$h  x.msi\nnot a line\n${"b".repeat(10)}  y\n"))
        assertEquals(mapOf("x.msi" to h), Updater.parseSums("${h.uppercase()} *x.msi"))
    }
}

class UpdaterTest {
    private var now = 1_000_000_000L
    private fun updater(
        dl: FakeDownloader, dist: String = "desktop", version: String? = "0.2.1", data: AppData = AppData(),
        installer: Installer = Launches(), quit: () -> Unit = {}, dir: File = folder(),
    ) = Updater(NimboConfig(version = version, distribution = dist), data, dl, dir, installer, { now }, { 42L }, { "C:/Nimbo/Nimbo.exe" }, quit)

    @Test
    fun `finds the newest version and the installer for this installation`() = runBlocking {
        val u = updater(FakeDownloader(releasesJson("0.2.1", "0.3.0", "0.2.5")))
        val s = u.check(force = true)
        assertEquals("0.3.0", s.latest!!.version)
        assertTrue(s.available)
        assertEquals("Nimbo-0.3.0-windows-x64.msi", s.asset!!.name)
        assertTrue(s.canInstall || !System.getProperty("os.name").startsWith("Windows"))
        assertFalse(s.latest!!.assets.any { it.name == "evil.exe" }, "assets outside the repository's releases are dropped")
    }

    @Test
    fun `nothing is offered when already current or newer`() = runBlocking {
        assertFalse(updater(FakeDownloader(releasesJson("0.2.1"))).check(true).available)
        assertFalse(updater(FakeDownloader(releasesJson("0.2.1")), version = "0.3.0").check(true).available)
        // A development build without a version sees every release as news.
        assertTrue(updater(FakeDownloader(releasesJson("0.2.1")), version = null).check(true).available)
    }

    @Test
    fun `jar installs pick the jar and docker is told to pull`() = runBlocking {
        val jar = updater(FakeDownloader(releasesJson("0.3.0")), dist = "jar").check(true)
        assertEquals("nimbo-0.3.0.jar", jar.asset!!.name)
        assertContains(jar.manual!!, "nimbo-0.3.0.jar")
        val docker = updater(FakeDownloader(releasesJson("0.3.0")), dist = "docker").check(true)
        assertNull(docker.asset)
        assertContains(docker.manual!!, "docker compose pull")
        assertFalse(docker.canInstall)
    }

    @Test
    fun `manual mode never contacts anyone unless asked`() = runBlocking {
        val data = AppData()
        data.settings.patch(SettingsPatch(updateMode = "manual"))
        val dl = FakeDownloader(releasesJson("0.3.0"))
        val u = updater(dl, data = data)
        assertNull(u.check(force = false).latest)
        assertTrue(dl.asked.isEmpty())
        assertNotNull(u.check(force = true).latest)
        assertEquals(1, dl.asked.size)
    }

    @Test
    fun `automatic mode asks at most once a day`() = runBlocking {
        val dl = FakeDownloader(releasesJson("0.3.0"))
        val u = updater(dl)
        u.check(force = false)
        u.check(force = false)
        assertEquals(1, dl.asked.size)
        now += Updater.DAY_MS + 1
        u.check(force = false)
        assertEquals(2, dl.asked.size)
    }

    @Test
    fun `a failed check keeps the reason and does not crash`() = runBlocking {
        val u = updater(FakeDownloader("[]", fail = "GitHub answered 403"))
        val s = u.check(true)
        assertEquals("GitHub answered 403", s.error)
        assertNull(s.latest)
        assertEquals("No published versions were found", updater(FakeDownloader("[]")).check(true).error)
        assertContains(updater(FakeDownloader("not json")).check(true).error.orEmpty(), "Could not reach GitHub")
    }

    @Test
    fun `download verifies the checksum and keeps the file`() = runBlocking {
        val dir = folder()
        val u = updater(FakeDownloader(releasesJson("0.3.0")), dir = dir)
        u.check(true)
        val f = u.download("0.3.0")
        assertTrue(f.verified)
        assertEquals(sha(MSI_BYTES), f.sha256)
        assertEquals(MSI_BYTES, File(dir, "updates/Nimbo-0.3.0-windows-x64.msi").readText())
        assertFalse(File(dir, "updates/Nimbo-0.3.0-windows-x64.msi.part").exists())
        assertEquals(listOf("0.3.0"), u.files().map { it.version })
    }

    @Test
    fun `a file that does not match is deleted and refused`() = runBlocking {
        val dir = folder()
        val dl = FakeDownloader(releasesJson("0.3.0"), files = mutableMapOf("Nimbo-0.3.0-windows-x64.msi" to "tampered"))
        val u = updater(dl, dir = dir)
        u.check(true)
        val e = assertFailsWith<UpdateError> { u.download("0.3.0") }
        assertContains(e.message!!, "does not match")
        assertEquals(emptyList(), File(dir, "updates").list()?.toList().orEmpty())
        assertTrue(u.files().isEmpty())
    }

    @Test
    fun `download refuses unknown versions, missing sums and unlisted files`() = runBlocking {
        val dl = FakeDownloader(releasesJson("0.3.0"))
        val u = updater(dl)
        assertFailsWith<InvalidParam> { u.download("0.3.0") }
        u.check(true)
        assertFailsWith<InvalidParam> { u.download("9.9.9") }
        dl.files["SHA256SUMS-0.3.0"] = "${"a".repeat(64)}  something-else.msi\n"
        assertContains(assertFailsWith<UpdateError> { u.download("0.3.0") }.message!!, "does not list")
        assertFailsWith<InvalidParam> { updater(FakeDownloader(releasesJson("0.3.0")), dist = "docker").also { it.check(true) }.download("0.3.0") }
    }

    @Test
    fun `import checks the hash you give or the sums file beside it`() = runBlocking {
        val src = folder()
        val installer = File(src, "Nimbo-0.4.0-windows-x64.msi").apply { writeText(MSI_BYTES) }
        val u = updater(FakeDownloader("[]"))

        val unverified = u.import(installer.path, null)
        assertFalse(unverified.verified)
        assertEquals("0.4.0", unverified.version)

        assertTrue(u.import(installer.path, sha(MSI_BYTES).uppercase()).verified)
        assertContains(assertFailsWith<UpdateError> { u.import(installer.path, "0".repeat(64)) }.message!!, "does not match")
        assertFailsWith<InvalidParam> { u.import(installer.path, "xyz") }

        File(src, "SHA256SUMS").writeText("${"1".repeat(64)}  ${installer.name}\n")
        assertFailsWith<UpdateError> { u.import(installer.path, null) }
        File(src, "SHA256SUMS").writeText("${sha(MSI_BYTES)}  ${installer.name}\n")
        assertTrue(u.import(installer.path, null).verified)
    }

    @Test
    fun `import refuses odd files`() = runBlocking {
        val src = folder()
        val u = updater(FakeDownloader("[]"))
        assertFailsWith<InvalidParam> { u.import(File(src, "missing.msi").path, null) }
        val odd = File(src, "setup.exe").apply { writeText("x") }
        assertContains(assertFailsWith<InvalidParam> { u.import(odd.path, null) }.message!!, "does not look like a Nimbo installer")
    }

    @Test
    fun `install needs confirmation, the desktop app, a newer version and an untouched file`() = runBlocking {
        val launches = Launches()
        var quits = 0
        val dir = folder()
        val u = updater(FakeDownloader(releasesJson("0.3.0")), installer = launches, quit = { quits++ }, dir = dir)
        u.check(true)
        u.download("0.3.0")

        assertFailsWith<InvalidParam> { u.install("0.3.0", confirm = false) }
        assertTrue(launches.calls.isEmpty())
        assertFailsWith<UnknownTarget> { u.install("0.9.0", confirm = true) }

        File(dir, "updates/Nimbo-0.3.0-windows-x64.msi").writeText("swapped after the check")
        assertContains(assertFailsWith<UpdateError> { u.install("0.3.0", true) }.message!!, "changed on disk")
        assertTrue(launches.calls.isEmpty())

        u.download("0.3.0")
        u.install("0.3.0", confirm = true)
        assertEquals(1, launches.calls.size)
        assertEquals(1, quits)
    }

    @Test
    fun `install is refused outside the desktop app and for older versions`() = runBlocking {
        val jar = updater(FakeDownloader(releasesJson("0.3.0")), dist = "jar")
        assertFailsWith<InvalidParam> { jar.install("0.3.0", true) }

        val dir = folder()
        val old = updater(FakeDownloader("[]"), version = "0.5.0", dir = dir)
        val installer = File(folder(), "Nimbo-0.3.0-windows-x64.msi").apply { writeText(MSI_BYTES) }
        old.import(installer.path, null)
        assertContains(assertFailsWith<InvalidParam> { old.install("0.3.0", true) }.message!!, "not newer")
    }

    @Test
    fun `clean removes the downloaded installers`() = runBlocking {
        val dir = folder()
        val u = updater(FakeDownloader(releasesJson("0.3.0")), dir = dir)
        u.check(true); u.download("0.3.0")
        assertEquals(1, u.clean())
        assertTrue(u.files().isEmpty())
        assertEquals(emptyList(), File(dir, "updates").list()?.toList().orEmpty())
    }

    @Test
    fun `the powershell script receives paths as parameters, not as code`() {
        val dir = folder()
        // Only the script is written and read; launching is never exercised, so no test can install anything.
        val script = WindowsInstaller(dir).writeScript().readText()
        assertContains(script, "param(")
        assertContains(script, "msiexec.exe")
        assertContains(script, "-LiteralPath")
        assertFalse("Invoke-Expression" in script || "iex " in script)
    }
}

class UpdateRoutesTest {
    private fun ktorUpdater(dl: FakeDownloader, dir: File = folder()) =
        Updater(NimboConfig(version = "0.2.1", distribution = "desktop"), AppData(), dl, dir, Launches(), { 0L }, { 1L }, { null }, {})

    @Test
    fun `the whole flow works over http and bad requests get clear answers`() = testApplication {
        val dl = FakeDownloader(releasesJson("0.3.0"))
        val updater = ktorUpdater(dl)
        application { nimboModule(NimboConfig(version = "0.2.1", distribution = "desktop"), FakeRuntime(), updater = updater, sampleSeconds = 3600) }

        assertContains(client.get("/api/updates").bodyAsText(), "\"available\":false")
        assertContains(client.post("/api/updates/check?force=true").bodyAsText(), "\"available\":true")

        val ok = client.post("/api/updates/download") { contentType(ContentType.Application.Json); setBody("""{"version":"0.3.0"}""") }
        assertEquals(HttpStatusCode.OK, ok.status)
        assertContains(ok.bodyAsText(), "\"verified\":true")

        val noConfirm = client.post("/api/updates/install") { contentType(ContentType.Application.Json); setBody("""{"version":"0.3.0"}""") }
        assertEquals(HttpStatusCode.BadRequest, noConfirm.status)
        val go = client.post("/api/updates/install") { contentType(ContentType.Application.Json); setBody("""{"version":"0.3.0","confirm":true}""") }
        assertEquals(HttpStatusCode.Accepted, go.status)

        val missing = client.post("/api/updates/import") { contentType(ContentType.Application.Json); setBody("""{"path":"Z:/nope.msi"}""") }
        assertEquals(HttpStatusCode.BadRequest, missing.status)
        assertEquals(HttpStatusCode.OK, client.delete("/api/updates/files").status)
    }

    @Test
    fun `a tampered download is a gateway error, not a crash`() = testApplication {
        val dl = FakeDownloader(releasesJson("0.3.0"), files = mutableMapOf("Nimbo-0.3.0-windows-x64.msi" to "tampered"))
        application { nimboModule(NimboConfig(version = "0.2.1", distribution = "desktop"), FakeRuntime(), updater = ktorUpdater(dl), sampleSeconds = 3600) }
        client.post("/api/updates/check?force=true")
        val r = client.post("/api/updates/download") { contentType(ContentType.Application.Json); setBody("""{"version":"0.3.0"}""") }
        assertEquals(HttpStatusCode.BadGateway, r.status)
        assertContains(r.bodyAsText(), "does not match")
    }

    @Test
    fun `routes are absent when no updater is configured`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), sampleSeconds = 3600) }
        assertEquals(HttpStatusCode.NotFound, client.get("/api/updates").status)
    }
}
