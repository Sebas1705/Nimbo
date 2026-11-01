package es.sebas1705.nimbo

import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpStatusCode
import io.ktor.server.testing.testApplication
import java.io.File
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/** Records order of calls and can fail specific providers. */
private class RecordingRuntime(
    private val failing: Set<String> = emptySet(),
    val live: MutableMap<String, ContainerInfo> = mutableMapOf(),
) : ContainerRuntime {
    val calls = mutableListOf<String>()
    override suspend fun status() = DockerStatusDto(true, "29.0")
    override suspend fun containers() = live.toMap()
    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>): Result<Unit> {
        calls += "start:${def.id}"
        if (def.id in failing) return Result.failure(IllegalStateException("port busy"))
        live[def.id] = ContainerInfo(def.id, true, "Up")
        return Result.success(Unit)
    }
    override suspend fun stop(def: ProviderDef): Result<Unit> {
        calls += "stop:${def.id}"
        live[def.id] = ContainerInfo(def.id, false, "Exited")
        return Result.success(Unit)
    }
    override suspend fun remove(def: ProviderDef) = Result.success(Unit)
    override suspend fun logs(def: ProviderDef, tail: Int) = Result.success("")
}

class StackParsingTest {
    @Test
    fun `parses valid stacks and keeps order`() {
        val catalog = StackCatalog.parse(
            """
            stacks:
              one:
                description: first
                providers: [redis, minio, redis]
              two:
                providers: [floci-aws]
            """.trimIndent(),
        )
        assertTrue(catalog.problems.isEmpty(), catalog.problems.toString())
        assertEquals(listOf("one", "two"), catalog.stacks.map { it.id })
        assertEquals(listOf("redis", "minio"), catalog.find("one")!!.providers, "duplicates are removed")
        assertEquals("", catalog.find("two")!!.description)
    }

    @Test
    fun `skips invalid stacks and reports why`() {
        val catalog = StackCatalog.parse(
            """
            stacks:
              ok:
                providers: [redis]
              ghost:
                providers: [redis, not-a-provider]
              empty:
                providers: []
              Bad_Id:
                providers: [redis]
            """.trimIndent(),
        )
        assertEquals(listOf("ok"), catalog.stacks.map { it.id })
        assertEquals(3, catalog.problems.size)
        assertTrue(catalog.problems.any { "not-a-provider" in it })
        assertTrue(catalog.problems.any { "Bad_Id" in it })
    }

    @Test
    fun `malformed yaml and missing root do not throw`() {
        assertTrue(StackCatalog.parse("stacks: [unclosed").problems.isNotEmpty())
        assertTrue(StackCatalog.parse("other: 1").problems.single().contains("stacks"))
        assertTrue(StackCatalog.parse("").stacks.isEmpty())
    }

    @Test
    fun `yaml cannot instantiate arbitrary classes`() {
        val catalog = StackCatalog.parse("stacks: !!java.lang.ProcessBuilder [[calc]]")
        assertTrue(catalog.stacks.isEmpty())
        assertTrue(catalog.problems.isNotEmpty())
    }

    @Test
    fun `the nimbo yml shipped in the repo is fully valid`() {
        val file = listOf(File("nimbo.yml"), File("../nimbo.yml")).first { it.isFile }
        val catalog = StackCatalog.parse(file.readText())
        assertTrue(catalog.problems.isEmpty(), catalog.problems.toString())
        assertTrue(catalog.stacks.size >= 3)
    }

    @Test
    fun `built-in stacks only reference known providers`() {
        StackCatalog.builtIn.stacks.forEach { stack ->
            stack.providers.forEach { assertNotNull(Catalog.find(it), "${stack.id}: $it") }
        }
    }
}

class ExportsTest {
    @Test
    fun `env groups endpoints per provider`() {
        val text = Exports.env(listOf(Catalog.find("floci-aws")!!, Catalog.find("redis")!!))
        assertContains(text, "# Floci AWS")
        assertContains(text, "AWS_ENDPOINT_URL=http://localhost:4566")
        assertContains(text, "REDIS_URL=redis://localhost:6379")
    }

    @Test
    fun `tofu override targets the floci port and skips real account lookups`() {
        val tf = Exports.tofuAwsOverride()
        assertContains(tf, "provider \"aws\"")
        assertContains(tf, "skip_credentials_validation = true")
        assertContains(tf, "skip_requesting_account_id  = true")
        assertTrue(Regex("""s3\s+= "http://localhost:4566"""").containsMatchIn(tf))
        assertEquals(tf.count { it == '{' }, tf.count { it == '}' }, "balanced braces")
    }

    @Test
    fun `aws cli profile has endpoint and dummy keys only`() {
        val ini = Exports.awsCliProfile()
        assertContains(ini, "[profile nimbo]")
        assertContains(ini, "endpoint_url = http://localhost:4566")
        assertFalse(ini.contains("AKIA"))
    }
}

class StackApiTest {
    private val stacks = StackCatalog.parse(
        """
        stacks:
          mix:
            description: test
            providers: [floci-aws, redis, minio]
          azure-only:
            providers: [azurite]
        """.trimIndent(),
    )

    @Test
    fun `lists stacks with aggregated state and problems`() = testApplication {
        val runtime = RecordingRuntime(live = mutableMapOf("redis" to ContainerInfo("redis", true, "Up")))
        application { nimboModule(NimboConfig(), runtime, stacks) }
        val body = client.get("/api/stacks").bodyAsText()
        assertContains(body, "\"id\":\"mix\"")
        assertContains(body, "\"state\":\"partial\"")
        assertContains(body, "\"running\":1")
        assertContains(body, "\"state\":\"stopped\"")
    }

    @Test
    fun `up starts in order and skips what is already running`() = testApplication {
        val runtime = RecordingRuntime(live = mutableMapOf("redis" to ContainerInfo("redis", true, "Up")))
        application { nimboModule(NimboConfig(), runtime, stacks) }
        val response = client.post("/api/stacks/mix/up")
        assertEquals(HttpStatusCode.OK, response.status)
        assertEquals(listOf("start:floci-aws", "start:minio"), runtime.calls)
        assertContains(client.get("/api/stacks").bodyAsText(), "\"state\":\"running\"")
    }

    @Test
    fun `up keeps going when one provider fails and reports it`() = testApplication {
        val runtime = RecordingRuntime(failing = setOf("redis"))
        application { nimboModule(NimboConfig(), runtime, stacks) }
        val body = client.post("/api/stacks/mix/up").bodyAsText()
        assertEquals(listOf("start:floci-aws", "start:redis", "start:minio"), runtime.calls)
        assertContains(body, "\"ok\":false")
        assertContains(body, "port busy")
    }

    @Test
    fun `down stops only running providers in reverse order`() = testApplication {
        val runtime = RecordingRuntime(
            live = mutableMapOf(
                "floci-aws" to ContainerInfo("floci-aws", true, "Up"),
                "redis" to ContainerInfo("redis", false, "Exited"),
                "minio" to ContainerInfo("minio", true, "Up"),
            ),
        )
        application { nimboModule(NimboConfig(), runtime, stacks) }
        client.post("/api/stacks/mix/down")
        assertEquals(listOf("stop:minio", "stop:floci-aws"), runtime.calls)
    }

    @Test
    fun `unknown stack is 404 and touches nothing`() = testApplication {
        val runtime = RecordingRuntime()
        application { nimboModule(NimboConfig(), runtime, stacks) }
        assertEquals(HttpStatusCode.NotFound, client.post("/api/stacks/nope/up").status)
        assertEquals(HttpStatusCode.NotFound, client.get("/api/stacks/nope/export?format=env").status)
        assertTrue(runtime.calls.isEmpty())
    }

    @Test
    fun `export validates format and requires an aws emulator for aws formats`() = testApplication {
        application { nimboModule(NimboConfig(), RecordingRuntime(), stacks) }
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/stacks/mix/export?format=xml").status)
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/stacks/mix/export").status)
        assertEquals(HttpStatusCode.Conflict, client.get("/api/stacks/azure-only/export?format=tofu").status)
        assertEquals(HttpStatusCode.OK, client.get("/api/stacks/azure-only/export?format=env").status)
        val tofu = client.get("/api/stacks/mix/export?format=tofu").bodyAsText()
        assertContains(tofu, "nimbo_override.tf")
        assertContains(tofu, "provider")
    }

    @Test
    fun `stacks are protected by the api token`() = testApplication {
        application { nimboModule(NimboConfig(apiToken = "t0ken"), RecordingRuntime(), stacks) }
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/stacks").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/stacks/mix/up").status)
    }
}
