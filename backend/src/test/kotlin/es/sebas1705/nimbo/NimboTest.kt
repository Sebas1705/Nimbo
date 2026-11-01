package es.sebas1705.nimbo

import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.server.testing.testApplication
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private class FakeRuntime(
    var docker: DockerStatusDto = DockerStatusDto(true, "29.0"),
    val live: MutableMap<String, ContainerInfo> = mutableMapOf(),
    var failStart: String? = null,
) : ContainerRuntime {
    override suspend fun status() = docker
    override suspend fun containers() = live.toMap()
    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>): Result<Unit> {
        failStart?.let { return Result.failure(IllegalStateException(it)) }
        live[def.id] = ContainerInfo(def.id, true, "Up")
        return Result.success(Unit)
    }
    override suspend fun stop(def: ProviderDef): Result<Unit> {
        live[def.id] = ContainerInfo(def.id, false, "Exited")
        return Result.success(Unit)
    }
    override suspend fun remove(def: ProviderDef): Result<Unit> {
        live.remove(def.id)
        return Result.success(Unit)
    }
    override suspend fun logs(def: ProviderDef, tail: Int) = Result.success("line-${def.id}-$tail")
}

class CatalogTest {
    @Test
    fun `ids are unique and host ports do not collide`() {
        val ids = Catalog.providers.map { it.id }
        assertEquals(ids.size, ids.toSet().size)
        val ports = Catalog.providers.flatMap { it.ports }
        assertEquals(ports.size, ports.toSet().size, "two providers publish the same host port")
    }

    @Test
    fun `secrets are referenced by name only`() {
        Catalog.providers.forEach { def ->
            def.secretEnv.values.forEach { assertTrue(it.startsWith("NIMBO_"), "${def.id}: $it") }
            def.containerEnv.values.forEach { assertFalse(it.contains("password", ignoreCase = true)) }
        }
    }
}

class RunArgsTest {
    @Test
    fun `run args are labelled, loopback-bound and carry secrets from host env`() {
        val minio = Catalog.find("minio")!!
        val args = CliContainerRuntime.buildRunArgs(
            minio,
            mapOf("NIMBO_MINIO_USER" to "u", "NIMBO_MINIO_PASSWORD" to "pw-12345"),
        )
        assertEquals(listOf("run", "-d", "--name", "nimbo-minio"), args.take(4))
        assertContains(args, "nimbo.managed=true")
        assertContains(args, "127.0.0.1:9000:9000")
        assertContains(args, "MINIO_ROOT_PASSWORD=pw-12345")
        assertEquals(listOf("server", "/data", "--console-address", ":9001"), args.takeLast(4))
    }

    @Test
    fun `start refuses when a secret is missing and never calls docker`() = runBlocking {
        var called = false
        val runtime = CliContainerRuntime { _, _ -> called = true; ExecResult(0, "") }
        val result = runtime.start(Catalog.find("postgres")!!, emptyMap())
        assertTrue(result.isFailure)
        assertContains(result.exceptionOrNull()!!.message!!, "NIMBO_POSTGRES_PASSWORD")
        assertFalse(called)
    }

    @Test
    fun `start reuses an existing stopped container`() = runBlocking {
        val calls = mutableListOf<List<String>>()
        val runtime = CliContainerRuntime { args, _ ->
            calls += args
            if (args.first() == "ps") ExecResult(0, "nimbo-redis|exited|Exited (0) 1 minute ago") else ExecResult(0, "")
        }
        assertTrue(runtime.start(Catalog.find("redis")!!, emptyMap()).isSuccess)
        assertEquals(listOf("start", "nimbo-redis"), calls.last())
    }

    @Test
    fun `ps format has no quotes so it survives Windows argument quoting`() = runBlocking {
        val calls = mutableListOf<List<String>>()
        val runtime = CliContainerRuntime { args, _ -> calls += args; ExecResult(0, "nimbo-redis|running|Up 3 seconds") }
        val live = runtime.containers()
        assertTrue(live.getValue("redis").running)
        assertFalse(calls.single().any { '"' in it }, "docker args must not contain double quotes")
    }

    @Test
    fun `status reports unavailable when docker info fails`() = runBlocking {
        val runtime = CliContainerRuntime { _, _ -> ExecResult(1, "boom") }
        assertFalse(runtime.status().available)
    }
}

class ApiTest {
    @Test
    fun `lists every catalog provider with its live state`() = testApplication {
        val runtime = FakeRuntime(live = mutableMapOf("redis" to ContainerInfo("redis", true, "Up 2 minutes")))
        application { nimboModule(NimboConfig(), runtime) }
        val body = client.get("/api/providers").bodyAsText()
        Catalog.providers.forEach { assertContains(body, "\"id\":\"${it.id}\"") }
        assertContains(body, "\"state\":\"running\"")
        assertContains(body, "\"state\":\"not_created\"")
    }

    @Test
    fun `start then stop updates state`() = testApplication {
        val runtime = FakeRuntime()
        application { nimboModule(NimboConfig(), runtime) }
        assertEquals(HttpStatusCode.Accepted, client.post("/api/providers/redis/start").status)
        assertTrue(runtime.live.getValue("redis").running)
        assertEquals(HttpStatusCode.Accepted, client.post("/api/providers/redis/stop").status)
        assertFalse(runtime.live.getValue("redis").running)
    }

    @Test
    fun `start failure maps to 409 with the reason`() = testApplication {
        val runtime = FakeRuntime(failStart = "port is already allocated")
        application { nimboModule(NimboConfig(), runtime) }
        val response = client.post("/api/providers/redis/start")
        assertEquals(HttpStatusCode.Conflict, response.status)
        assertContains(response.bodyAsText(), "port is already allocated")
    }

    @Test
    fun `unknown provider is 404 and nothing reaches docker`() = testApplication {
        val runtime = FakeRuntime()
        application { nimboModule(NimboConfig(), runtime) }
        assertEquals(HttpStatusCode.NotFound, client.post("/api/providers/..%2Fevil/start").status)
        assertEquals(HttpStatusCode.NotFound, client.get("/api/providers/nope/logs").status)
        assertTrue(runtime.live.isEmpty())
    }

    @Test
    fun `env endpoint renders dotenv`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime()) }
        val body = client.get("/api/providers/floci-aws/env").bodyAsText()
        assertContains(body, "AWS_ENDPOINT_URL")
        assertContains(body, "http://localhost:4566")
    }

    @Test
    fun `docker down yields empty live state instead of failing`() = testApplication {
        val runtime = FakeRuntime(docker = DockerStatusDto(false, error = "down"))
        application { nimboModule(NimboConfig(), runtime) }
        assertEquals(HttpStatusCode.OK, client.get("/api/providers").status)
        assertContains(client.get("/api/docker").bodyAsText(), "\"available\":false")
    }

    @Test
    fun `token protects the api but not health`() = testApplication {
        application { nimboModule(NimboConfig(apiToken = "s3cret"), FakeRuntime()) }
        val health = client.get("/api/health")
        assertEquals(HttpStatusCode.OK, health.status)
        assertContains(health.bodyAsText(), "\"authRequired\":true")
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/providers").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/providers/redis/start").status)
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/providers") { header(HttpHeaders.Authorization, "Bearer nope") }.status)
        assertEquals(HttpStatusCode.OK, client.get("/api/providers") { header(HttpHeaders.Authorization, "Bearer s3cret") }.status)
    }
}
