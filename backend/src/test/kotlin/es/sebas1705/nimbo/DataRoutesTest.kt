package es.sebas1705.nimbo

import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.request.put
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.server.testing.ApplicationTestBuilder
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** A runtime that remembers how it was asked to start things and which environment it got. */
internal class PersistingRuntime : ContainerRuntime {
    val live = mutableMapOf<String, ContainerInfo>()
    val started = mutableListOf<Triple<String, Boolean, Map<String, String>>>()
    val saved = mutableSetOf<String>()
    var deleted = mutableListOf<String>()
    override suspend fun status() = DockerStatusDto(true, "29.0")
    override suspend fun containers() = live.toMap()
    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>) = start(def, hostEnv, false)
    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>, persist: Boolean): Result<Unit> {
        started += Triple(def.id, persist, hostEnv)
        live[def.id] = ContainerInfo(def.id, true, "Up", persist)
        if (persist) saved += def.id
        return Result.success(Unit)
    }
    override suspend fun stop(def: ProviderDef): Result<Unit> { live[def.id] = ContainerInfo(def.id, false, "Exited"); return Result.success(Unit) }
    override suspend fun remove(def: ProviderDef): Result<Unit> { live.remove(def.id); return Result.success(Unit) }
    override suspend fun logs(def: ProviderDef, tail: Int) = Result.success("")
    override suspend fun savedData() = saved.toSet()
    override suspend fun deleteData(def: ProviderDef): Result<Unit> {
        if (live[def.id]?.running == true) return Result.failure(IllegalStateException("Stop ${def.name} before deleting its data"))
        saved -= def.id; deleted += def.id
        return Result.success(Unit)
    }
}

private val json = Json { ignoreUnknownKeys = true }

class DataRoutesTest {
    private fun ApplicationTestBuilder.app(
        data: AppData = AppData(),
        secrets: SecretStore = MemorySecrets(),
        runtime: ContainerRuntime = PersistingRuntime(),
        config: NimboConfig = NimboConfig(),
    ) {
        application { nimboModule(config, runtime, data = data, secrets = secrets, sampleSeconds = 3600) }
    }

    private suspend fun ApplicationTestBuilder.putJson(path: String, body: String) =
        client.put(path) { contentType(ContentType.Application.Json); setBody(body) }

    private suspend fun ApplicationTestBuilder.postJson(path: String, body: String) =
        client.post(path) { contentType(ContentType.Application.Json); setBody(body) }

    @Test
    fun `settings can be read, changed and are validated`() = testApplication {
        app()
        assertEquals("auto", json.parseToJsonElement(client.get("/api/settings").bodyAsText()).jsonObject["theme"]!!.jsonPrimitive.content)
        assertEquals(HttpStatusCode.OK, putJson("/api/settings", """{"lang":"en","theme":"dark"}""").status)
        val now = json.parseToJsonElement(client.get("/api/settings").bodyAsText()).jsonObject
        assertEquals("en", now["lang"]!!.jsonPrimitive.content)
        assertEquals("dark", now["theme"]!!.jsonPrimitive.content)
        assertEquals(HttpStatusCode.BadRequest, putJson("/api/settings", """{"lang":"xx"}""").status)
    }

    @Test
    fun `lab progress is imported once from the browser`() = testApplication {
        app()
        val first = postJson("/api/progress/import", """{"ids":["lab-a","lab-b"]}""")
        assertEquals(HttpStatusCode.OK, first.status)
        assertContains(client.get("/api/progress").bodyAsText(), "lab-b")
    }

    @Test
    fun `starting and stopping providers feeds the statistics`() = testApplication {
        val data = AppData()
        app(data)
        client.post("/api/providers/redis/start")
        client.post("/api/providers/redis/stop")
        client.post("/api/stacks/${StackCatalog.builtIn.stacks.first().id}/up")
        val view = json.parseToJsonElement(client.get("/api/stats").bodyAsText()).jsonObject
        val redis = view["stats"]!!.jsonObject["providers"]!!.jsonObject["redis"]!!.jsonObject
        assertTrue(redis["starts"]!!.jsonPrimitive.content.toInt() >= 1)
        assertEquals("1", redis["stops"]!!.jsonPrimitive.content)
        assertTrue(view["journal"]!!.jsonArray.size >= 3)
    }

    @Test
    fun `the persistence switch is remembered and used on the next start`() = testApplication {
        val runtime = PersistingRuntime()
        app(runtime = runtime)
        assertEquals(HttpStatusCode.OK, postJson("/api/providers/postgres/persist", """{"enabled":true}""").status)
        postJson("/api/providers/redis/persist", """{"enabled":true}""")
        client.post("/api/providers/redis/start")
        assertEquals(true, runtime.started.last().second)
        val listed = client.get("/api/providers").bodyAsText()
        val redis = json.parseToJsonElement(listed).jsonArray.first { it.jsonObject["id"]!!.jsonPrimitive.content == "redis" }.jsonObject
        assertTrue(redis["persistable"]!!.jsonPrimitive.boolean)
        assertTrue(redis["persist"]!!.jsonPrimitive.boolean)
        assertTrue(redis["hasData"]!!.jsonPrimitive.boolean)
    }

    @Test
    fun `persistence of an unknown or stateless provider is refused`() = testApplication {
        app()
        assertEquals(HttpStatusCode.NotFound, postJson("/api/providers/nope/persist", """{"enabled":true}""").status)
        val stateless = Catalog.providers.first { it.persist == null }
        assertEquals(HttpStatusCode.BadRequest, postJson("/api/providers/${stateless.id}/persist", """{"enabled":true}""").status)
    }

    @Test
    fun `deleting data needs confirmation and a stopped provider`() = testApplication {
        val runtime = PersistingRuntime()
        app(runtime = runtime)
        postJson("/api/providers/redis/persist", """{"enabled":true}""")
        client.post("/api/providers/redis/start")

        assertEquals(HttpStatusCode.BadRequest, client.delete("/api/providers/redis/data").status)
        assertEquals(HttpStatusCode.Conflict, client.delete("/api/providers/redis/data?confirm=yes").status, "running")
        client.post("/api/providers/redis/stop")
        assertEquals(HttpStatusCode.NoContent, client.delete("/api/providers/redis/data?confirm=yes").status)
        assertEquals(listOf("redis"), runtime.deleted)
        assertEquals(HttpStatusCode.NotFound, client.delete("/api/providers/nope/data?confirm=yes").status)
    }

    @Test
    fun `stored secrets reach the provider and the environment wins`() = testApplication {
        val runtime = PersistingRuntime()
        val secrets = MemorySecrets().also { it.values["NIMBO_POSTGRES_PASSWORD"] = "stored-pw" }
        app(secrets = secrets, runtime = runtime)
        client.post("/api/providers/postgres/start")
        assertEquals("stored-pw", runtime.started.last().third["NIMBO_POSTGRES_PASSWORD"])
    }

    @Test
    fun `the environment wins over a stored secret`() = testApplication {
        val runtime = PersistingRuntime()
        val secrets = MemorySecrets().also { it.values["NIMBO_POSTGRES_PASSWORD"] = "stored-pw" }
        app(secrets = secrets, runtime = runtime, config = NimboConfig(hostEnv = mapOf("NIMBO_POSTGRES_PASSWORD" to "env-pw")))
        client.post("/api/providers/postgres/start")
        assertEquals("env-pw", runtime.started.last().third["NIMBO_POSTGRES_PASSWORD"])
    }

    @Test
    fun `secrets list names and flags but never values`() = testApplication {
        val secrets = MemorySecrets().also { it.values["NIMBO_MINIO_PASSWORD"] = "very-secret-pw" }
        app(secrets = secrets)
        val body = client.get("/api/secrets").bodyAsText()
        assertFalse("very-secret-pw" in body)
        val items = json.parseToJsonElement(body).jsonObject["items"]!!.jsonArray.associate { it.jsonObject["name"]!!.jsonPrimitive.content to it.jsonObject["set"]!!.jsonPrimitive.boolean }
        assertEquals(true, items["NIMBO_MINIO_PASSWORD"])
        assertEquals(false, items["NIMBO_POSTGRES_PASSWORD"])
    }

    @Test
    fun `secrets can be set, validated, generated and deleted`() = testApplication {
        val secrets = MemorySecrets()
        val data = AppData()
        app(data, secrets)
        assertEquals(HttpStatusCode.NoContent, putJson("/api/secrets/NIMBO_POSTGRES_PASSWORD", """{"value":"hunter2-hunter2"}""").status)
        assertEquals("hunter2-hunter2", secrets.values["NIMBO_POSTGRES_PASSWORD"])
        assertEquals(HttpStatusCode.BadRequest, putJson("/api/secrets/NIMBO_MINIO_PASSWORD", """{"value":"short"}""").status)
        assertEquals(HttpStatusCode.NotFound, putJson("/api/secrets/HOME", """{"value":"x"}""").status)

        val generated = json.parseToJsonElement(client.post("/api/secrets/NIMBO_MINIO_PASSWORD/generate").bodyAsText()).jsonObject["value"]!!.jsonPrimitive.content
        assertEquals(generated, secrets.values["NIMBO_MINIO_PASSWORD"])
        assertEquals(HttpStatusCode.BadRequest, client.post("/api/secrets/NIMBO_MINIO_USER/generate").status, "users are chosen, not generated")

        assertEquals(HttpStatusCode.NoContent, client.delete("/api/secrets/NIMBO_POSTGRES_PASSWORD").status)
        assertFalse("NIMBO_POSTGRES_PASSWORD" in secrets.values)
        assertEquals(HttpStatusCode.NotFound, client.delete("/api/secrets/HOME").status)

        // The journal remembers that a secret changed, never what it was.
        val journal = client.get("/api/stats").bodyAsText()
        assertContains(journal, "secret.set")
        assertFalse(generated in journal || "hunter2-hunter2" in journal)
    }

    @Test
    fun `an unavailable store says why`() = testApplication {
        app(secrets = UnavailableSecretStore("not here"))
        assertEquals(HttpStatusCode.BadRequest, putJson("/api/secrets/NIMBO_MINIO_USER", """{"value":"admin"}""").status)
        val body = json.parseToJsonElement(client.get("/api/secrets").bodyAsText()).jsonObject
        assertEquals(false, body["available"]!!.jsonPrimitive.boolean)
        assertEquals("not here", body["reason"]!!.jsonPrimitive.content)
    }

    @Test
    fun `backup round trips through the api and carries no secrets`() = testApplication {
        val data = AppData()
        app(data, MemorySecrets().also { it.values["NIMBO_MINIO_USER"] = "admin" })
        putJson("/api/settings", """{"lang":"en"}""")
        postJson("/api/progress/import", """{"ids":["lab-a"]}""")
        val backup = client.get("/api/data/export").bodyAsText()
        assertFalse("NIMBO_MINIO_USER" in backup)

        assertEquals(HttpStatusCode.NoContent, client.post("/api/data/reset?what=all&confirm=yes").status)
        assertTrue(data.progress.completed().isEmpty())
        assertEquals(HttpStatusCode.OK, postJson("/api/data/import", backup).status)
        assertEquals("en", data.settings.get().lang)
        assertEquals(setOf("lab-a"), data.progress.completed())

        assertEquals(HttpStatusCode.BadRequest, postJson("/api/data/import", """{"format":1,"exportedAt":1,"files":{"settings.json":"nope"}}""").status)
        assertEquals("en", data.settings.get().lang)
    }

    @Test
    fun `reset needs confirm and a known target`() = testApplication {
        val data = AppData()
        app(data)
        client.post("/api/providers/redis/start")
        postJson("/api/progress/import", """{"ids":["lab-a"]}""")

        assertEquals(HttpStatusCode.BadRequest, client.post("/api/data/reset?what=stats").status)
        assertEquals(HttpStatusCode.BadRequest, client.post("/api/data/reset?what=everything&confirm=yes").status)
        assertEquals(HttpStatusCode.NoContent, client.post("/api/data/reset?what=stats&confirm=yes").status)
        assertTrue(data.stats.stats().providers.isEmpty())
        assertEquals(setOf("lab-a"), data.progress.completed(), "resetting statistics keeps the progress")
        assertEquals(HttpStatusCode.NoContent, client.post("/api/data/reset?what=all&confirm=yes").status)
        assertTrue(data.progress.completed().isEmpty())
    }

    @Test
    fun `data info shows where things live without exposing contents`() = testApplication {
        app(config = NimboConfig(distribution = "desktop"))
        val info = json.parseToJsonElement(client.get("/api/data").bodyAsText()).jsonObject
        assertEquals("desktop", info["distribution"]!!.jsonPrimitive.content)
    }

    @Test
    fun `the api token protects the new routes too`() = testApplication {
        app(config = NimboConfig(apiToken = "tok"))
        listOf("/api/settings", "/api/stats", "/api/secrets", "/api/data", "/api/data/export", "/api/progress").forEach {
            assertEquals(HttpStatusCode.Unauthorized, client.get(it).status, it)
        }
        assertEquals(HttpStatusCode.OK, client.get("/api/secrets") { header(HttpHeaders.Authorization, "Bearer tok") }.status)
    }

    @Test
    fun `a foreign host header is refused before anything is read`() = testApplication {
        app()
        assertEquals(HttpStatusCode.Forbidden, client.get("/api/secrets") { header(HttpHeaders.Host, "evil.example") }.status)
    }
}
