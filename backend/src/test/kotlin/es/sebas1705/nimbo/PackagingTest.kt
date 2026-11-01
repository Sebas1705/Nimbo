package es.sebas1705.nimbo

import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpStatusCode
import io.ktor.server.testing.testApplication
import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PackagingConfigTest {
    @Test
    fun `defaults are loopback and localhost`() {
        val c = NimboConfig.fromEnv(emptyMap())
        assertEquals("127.0.0.1", c.host)
        assertEquals("localhost", c.emulatorHost)
        assertEquals(8080, c.port)
    }

    @Test
    fun `host and emulator host come from the environment, blank values fall back`() {
        val c = NimboConfig.fromEnv(mapOf("NIMBO_HOST" to "0.0.0.0", "NIMBO_EMULATOR_HOST" to "host.docker.internal", "NIMBO_PORT" to "9000"))
        assertEquals("0.0.0.0", c.host)
        assertEquals("host.docker.internal", c.emulatorHost)
        assertEquals(9000, c.port)
        val blank = NimboConfig.fromEnv(mapOf("NIMBO_HOST" to "  ", "NIMBO_EMULATOR_HOST" to ""))
        assertEquals("127.0.0.1", blank.host)
        assertEquals("localhost", blank.emulatorHost)
    }

    @Test
    fun `the version is reported by the health endpoint`() = testApplication {
        application { nimboModule(NimboConfig(version = "9.9.9"), FakeRuntime()) }
        assertContains(client.get("/api/health").bodyAsText(), "\"version\":\"9.9.9\"")
    }

    @Test
    fun `a development run has no version instead of a made up one`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime()) }
        assertContains(client.get("/api/health").bodyAsText(), "\"version\":null")
    }

    @Test
    fun `the declared version has release notes in the changelog`() {
        val gradle = listOf(File("build.gradle.kts"), File("backend/build.gradle.kts")).first { it.isFile }.readText()
        val declared = Regex("""\?: "(\d[^"]*)"""").find(gradle)!!.groupValues[1]
        val changelog = listOf(File("CHANGELOG.md"), File("../CHANGELOG.md")).first { it.isFile }.readText()
        val section = changelog.substringAfter("## [$declared]", missingDelimiterValue = "").substringBefore("\n## [")
        assertTrue(section.trim().isNotEmpty(), "CHANGELOG.md needs a '## [$declared]' section: the release workflow publishes it as the notes")
    }
}

class BundledResourcesTest {
    @Test
    fun `the bundled ui is served when there is no web directory`() = testApplication {
        application { nimboModule(NimboConfig(webResources = "fixture-static"), FakeRuntime()) }
        val response = client.get("/")
        assertEquals(HttpStatusCode.OK, response.status)
        assertContains(response.bodyAsText(), "fixture ui")
        assertContains(client.get("/index.html").bodyAsText(), "fixture ui")
    }

    @Test
    fun `a web directory on disk wins over the bundled ui`() = testApplication {
        val dir = Files.createTempDirectory("ui").toFile()
        try {
            File(dir, "index.html").writeText("<title>from disk</title>")
            application { nimboModule(NimboConfig(webDir = dir, webResources = "fixture-static"), FakeRuntime()) }
            assertContains(client.get("/").bodyAsText(), "from disk")
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun `without any ui the api still works and the root is a 404`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime()) }
        assertEquals(HttpStatusCode.NotFound, client.get("/").status)
        assertEquals(HttpStatusCode.OK, client.get("/api/health").status)
    }

    @Test
    fun `the bundled ui is still behind the host guard`() = testApplication {
        application { nimboModule(NimboConfig(webResources = "fixture-static"), FakeRuntime()) }
        val evil = client.get("/") { headers.append("Host", "evil.example") }
        assertEquals(HttpStatusCode.Forbidden, evil.status)
    }

    @Test
    fun `bundled labs are read from the index, a broken one is reported and a missing one is ignored`() {
        val catalog = LabCatalog.loadBundled("/fixture-defaults/labs")
        assertEquals(listOf("fixture-lab"), catalog.labs.map { it.dto.id })
        assertEquals(1, catalog.problems.size)
        assertContains(catalog.problems.single(), "broken.yml")
    }

    @Test
    fun `no bundled labs means an empty catalog, not a crash`() {
        val catalog = LabCatalog.loadBundled("/does-not-exist/labs")
        assertTrue(catalog.labs.isEmpty() && catalog.problems.isEmpty())
    }

    @Test
    fun `bundled stacks are parsed and a missing bundle is null`() {
        val stacks = StackCatalog.loadBundled("/fixture-defaults/nimbo.yml")
        assertNotNull(stacks)
        assertEquals(listOf("bundled"), stacks.stacks.map { it.id })
        assertNull(StackCatalog.loadBundled("/nope/nimbo.yml"))
    }

    @Test
    fun `every lab and stack in the repo would be packed, and nothing else sneaks in`() {
        val labs = listOf(File("labs"), File("../labs")).first { it.isDirectory }
        val yml = labs.listFiles { f -> f.extension == "yml" }!!.map { it.name }
        assertTrue(yml.isNotEmpty())
        assertEquals(yml.size, yml.toSet().size)
        assertTrue(labs.listFiles()!!.all { it.extension == "yml" }, "the labs folder must hold only lab files, they are all bundled")
    }
}

/** Records every URL requested through the shared gateway. */
private class UrlLog : HttpGateway {
    val urls = mutableListOf<String>()
    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply {
        urls += url
        return HttpReply(200, "{}")
    }
}

class EmulatorHostTest {
    @Test
    fun `every service talks to the configured emulator host, never to localhost`() = testApplication {
        val log = UrlLog()
        application {
            nimboFromConfig(
                NimboConfig(emulatorHost = "box.internal"), FakeRuntime(), http = log,
                shell = Shell { _, _, _ -> ExecResult(1, "No such container") },
                stacks = StackCatalog.builtIn, labs = LabCatalog(emptyList()),
            )
        }
        client.get("/api/inspect/aws/s3/buckets")
        client.get("/api/inspect/gcp/pubsub")
        client.get("/api/chaos")
        client.get("/api/traces/services")
        client.post("/api/traces/sample")
        client.post("/api/fidelity/run?suite=s3")
        client.post("/api/fidelity/run?suite=blob")
        client.post("/api/fidelity/run?suite=pubsub")

        assertTrue(log.urls.isNotEmpty())
        assertTrue(log.urls.all { it.startsWith("http://box.internal:") }, log.urls.filterNot { it.startsWith("http://box.internal:") }.toString())
        val ports = log.urls.map { it.substringAfter("box.internal:").substringBefore('/').toInt() }.toSet()
        assertEquals(setOf(4566, 4577, 4588, 8474, 16686, 4318), ports)
        assertFalse(log.urls.any { "localhost" in it })
    }

    @Test
    fun `without configuration the emulators are on localhost`() = testApplication {
        val log = UrlLog()
        application {
            nimboFromConfig(NimboConfig(), FakeRuntime(), http = log, shell = Shell { _, _, _ -> ExecResult(1, "") }, stacks = StackCatalog.builtIn, labs = LabCatalog(emptyList()))
        }
        client.get("/api/inspect/aws/s3/buckets")
        client.get("/api/chaos")
        assertTrue(log.urls.all { it.startsWith("http://localhost:") })
    }
}
