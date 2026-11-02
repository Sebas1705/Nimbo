package es.sebas1705.nimbo

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
import kotlin.test.assertNull
import kotlin.test.assertTrue

private fun dir() = Files.createTempDirectory("nimbo-img").toFile().also { it.deleteOnExit() }

private class FakeDocker(var images: String = "", var loadOut: String = "Loaded image: redis:7-alpine", var fail: Boolean = false) : Exec {
    val calls = mutableListOf<List<String>>()
    override suspend fun run(args: List<String>, timeoutSeconds: Long): ExecResult {
        calls += args
        if (fail) return ExecResult(1, "daemon not running")
        return when (args.first()) {
            "images" -> ExecResult(0, images)
            "load" -> ExecResult(0, loadOut)
            else -> ExecResult(0, "")
        }
    }
}

class ImagePacksTest {
    @Test
    fun `every provider image is in exactly one pack and every pack image is a provider image`() {
        val packs = Images.defaultPacks()
        val catalog = Catalog.providers.map { it.image }
        catalog.forEach { image -> assertEquals(1, packs.count { image in it.images }, "image '$image' must be in exactly one pack") }
        packs.flatMap { it.images }.forEach { assertTrue(it in catalog, "pack lists '$it' which no provider uses") }
        assertEquals(packs.map { it.id }.size, packs.map { it.id }.toSet().size)
    }

    @Test
    fun `pack file names map to their pack`() {
        assertEquals("data", Images.packOf("Nimbo-0.3.0-images-data.tar.gz"))
        assertNull(Images.packOf("random.tar"))
        assertTrue(Images.isPack("x.tar") && Images.isPack("x.TAR.GZ") && Images.isPack("x.tgz"))
        assertFalse(Images.isPack("x.zip") || Images.isPack("x.exe"))
    }

    @Test
    fun `an image without a tag means latest`() {
        assertEquals("floci/floci:latest", Images.normalize("floci/floci"))
        assertEquals("postgres:17-alpine", Images.normalize("postgres:17-alpine"))
        assertEquals("localhost:5000/x:latest", Images.normalize("localhost:5000/x"))
    }
}

class ImagesServiceTest {
    private fun service(docker: FakeDocker, vararg folders: File, data: AppData = AppData()) = Images(docker, { folders.toList() }, data)

    @Test
    fun `status says which images are present`() = runBlocking {
        val s = service(FakeDocker("redis:7-alpine\nfloci/floci:latest\nother:1")).status()
        assertTrue(s.docker)
        assertTrue(s.images.first { it.provider == "redis" }.present)
        assertTrue(s.images.first { it.provider == "floci-aws" }.present)
        assertFalse(s.images.first { it.provider == "postgres" }.present)
        assertEquals("data", s.images.first { it.provider == "redis" }.pack)
    }

    @Test
    fun `docker down is reported, not thrown`() = runBlocking {
        val s = service(FakeDocker(fail = true)).status()
        assertFalse(s.docker)
        assertTrue(s.images.none { it.present })
    }

    @Test
    fun `scan lists packs with their checksum state`() {
        val d = dir()
        val good = File(d, "Nimbo-0.3.0-images-data.tar.gz").apply { writeText("data pack") }
        val bad = File(d, "Nimbo-0.3.0-images-k8s.tar.gz").apply { writeText("k8s pack") }
        File(d, "notes.txt").writeText("ignored")
        File(d, "SHA256SUMS").writeText("${Updater.sha256(good)}  ${good.name}\n${"0".repeat(64)}  ${bad.name}\n")
        val found = service(FakeDocker(), d, File(d, "missing")).scan().associateBy { it.name }
        assertEquals(setOf(good.name, bad.name), found.keys)
        assertEquals(true, found.getValue(good.name).verified)
        assertEquals(false, found.getValue(bad.name).verified)
        assertEquals("data", found.getValue(good.name).pack)
    }

    @Test
    fun `scan without a sums file leaves verification unknown`() {
        val d = dir()
        File(d, "mine.tar").writeText("x")
        assertNull(service(FakeDocker(), d).scan().single().verified)
    }

    @Test
    fun `load runs docker load and reports what came in`() = runBlocking {
        val d = dir()
        val pack = File(d, "Nimbo-0.3.0-images-data.tar.gz").apply { writeText("x") }
        val docker = FakeDocker(loadOut = "Loaded image: redis:7-alpine\nLoaded image: postgres:17-alpine")
        val data = AppData()
        val r = service(docker, d, data = data).load(pack.path)
        assertEquals(listOf("redis:7-alpine", "postgres:17-alpine"), r.loaded)
        assertEquals(listOf("load", "-i", pack.path), docker.calls.last())
        assertContains(data.stats.journal().first().kind, "images.load")
    }

    @Test
    fun `load refuses a pack whose checksum does not match, before docker sees it`() = runBlocking {
        val d = dir()
        val pack = File(d, "Nimbo-0.3.0-images-data.tar.gz").apply { writeText("tampered") }
        File(d, "SHA256SUMS").writeText("${"a".repeat(64)}  ${pack.name}\n")
        val docker = FakeDocker()
        assertFailsWith<UpdateError> { service(docker, d).load(pack.path) }
        assertTrue(docker.calls.none { it.first() == "load" })
    }

    @Test
    fun `load refuses odd paths and reports docker errors`() = runBlocking {
        val d = dir()
        assertFailsWith<InvalidParam> { service(FakeDocker()).load(File(d, "nope.tar").path) }
        val text = File(d, "notes.txt").apply { writeText("x") }
        assertFailsWith<InvalidParam> { service(FakeDocker()).load(text.path) }
        val pack = File(d, "p.tar").apply { writeText("x") }
        assertFailsWith<UpdateError> { service(FakeDocker(fail = true)).load(pack.path) }
    }

    @Test
    fun `pull asks docker for the provider image only`() = runBlocking {
        val docker = FakeDocker()
        service(docker).pull("redis")
        assertEquals(listOf("pull", "redis:7-alpine"), docker.calls.last())
        assertFailsWith<UnknownTarget> { service(docker).pull("nope") }
    }

    @Test
    fun `search folders come from the environment, the app folder and the data folder`() {
        val app = dir()
        val data = AppData.onDisk(dir())
        val folders = Images.searchFolders(mapOf("NIMBO_IMAGES_DIR" to "X:/packs"), data, app)
        assertEquals(listOf(File("X:/packs"), File(app, "images"), File(data.store.location, "images")), folders)
        assertEquals(1, Images.searchFolders(emptyMap(), AppData.onDisk(dir()), null).size)
    }
}

class ImageRoutesTest {
    @Test
    fun `routes list, load and pull`() = testApplication {
        val d = dir()
        val pack = File(d, "Nimbo-0.3.0-images-data.tar.gz").apply { writeText("x") }
        val images = Images(FakeDocker(), { listOf(d) }, AppData())
        application { nimboModule(NimboConfig(), FakeRuntime(), images = images, sampleSeconds = 3600) }

        val list = client.get("/api/images").bodyAsText()
        assertContains(list, "Nimbo-0.3.0-images-data.tar.gz")
        val ok = client.post("/api/images/load") { contentType(ContentType.Application.Json); setBody("""{"path":${Json.quote(pack.path)}}""") }
        assertEquals(HttpStatusCode.OK, ok.status)
        val bad = client.post("/api/images/load") { contentType(ContentType.Application.Json); setBody("""{"path":"Z:/missing.tar"}""") }
        assertEquals(HttpStatusCode.BadRequest, bad.status)
        val garbled = client.post("/api/images/load") { contentType(ContentType.Application.Json); setBody("""{"path":"C:\Users\x"}""") }
        assertEquals(HttpStatusCode.BadRequest, garbled.status, "a malformed body is the caller's mistake, not a server error")
        assertEquals(HttpStatusCode.NoContent, client.post("/api/images/pull") { contentType(ContentType.Application.Json); setBody("""{"provider":"redis"}""") }.status)
        assertEquals(HttpStatusCode.NotFound, client.post("/api/images/pull") { contentType(ContentType.Application.Json); setBody("""{"provider":"nope"}""") }.status)
    }

    @Test
    fun `routes are absent when not configured`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), sampleSeconds = 3600) }
        assertEquals(HttpStatusCode.NotFound, client.get("/api/images").status)
    }
}

private object Json {
    fun quote(s: String) = kotlinx.serialization.json.JsonPrimitive(s).toString()
}
