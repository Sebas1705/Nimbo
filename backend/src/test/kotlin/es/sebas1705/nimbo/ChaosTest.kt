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
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Just enough of Toxiproxy's HTTP API to exercise [Chaos]; keeps state and records every call. */
private class FakeToxiproxy : HttpGateway {
    class Proxy(val name: String, val listen: String, val upstream: String, var enabled: Boolean, val toxics: MutableList<JsonObject> = mutableListOf())

    val proxies = linkedMapOf<String, Proxy>()
    val calls = mutableListOf<String>()
    var unreachable = false
    private val json = Json

    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply {
        if (unreachable) throw EmulatorUnreachable("down")
        val path = url.removePrefix("http://localhost:8474")
        calls += "$method $path"
        val parts = path.trim('/').split('/')
        val payload = body?.let { json.parseToJsonElement(it).jsonObject }
        return when {
            method == "GET" && path == "/proxies" -> HttpReply(200, render().toString())
            method == "POST" && path == "/reset" -> {
                proxies.values.forEach { it.enabled = true; it.toxics.clear() }
                HttpReply(204, "")
            }
            method == "POST" && path == "/proxies" -> {
                val p = payload!!
                proxies[p["name"]!!.jsonPrimitive.content] = Proxy(p["name"]!!.jsonPrimitive.content, p["listen"]!!.jsonPrimitive.content, p["upstream"]!!.jsonPrimitive.content, true)
                HttpReply(201, "{}")
            }
            method == "POST" && parts.size == 2 -> {
                val p = proxies[parts[1]] ?: return HttpReply(404, "no proxy")
                payload!!["enabled"]?.let { p.enabled = it.jsonPrimitive.content == "true" }
                HttpReply(200, "{}")
            }
            method == "POST" && parts.size == 3 && parts[2] == "toxics" -> {
                val p = proxies[parts[1]] ?: return HttpReply(404, "no proxy")
                p.toxics += payload!!
                HttpReply(200, payload.toString())
            }
            method == "DELETE" && parts.size == 4 -> {
                val p = proxies[parts[1]] ?: return HttpReply(404, "no proxy")
                val removed = p.toxics.removeAll { it["name"]!!.jsonPrimitive.content == parts[3] }
                if (removed) HttpReply(204, "") else HttpReply(404, "no toxic")
            }
            else -> HttpReply(400, "unsupported $method $path")
        }
    }

    private fun render() = buildJsonObject {
        proxies.forEach { (name, p) ->
            put(name, buildJsonObject {
                put("name", p.name); put("listen", p.listen); put("upstream", p.upstream); put("enabled", p.enabled)
                put("toxics", JsonArray(p.toxics))
            })
        }
    }

    fun writes() = calls.filter { !it.startsWith("GET") }
}

class ChaosServiceTest {
    @Test
    fun `view without toxiproxy reports unavailable but still lists targets and presets`() = runBlocking {
        val view = Chaos(FakeToxiproxy().apply { unreachable = true }).view()
        assertFalse(view.available)
        assertEquals(ChaosTargets.all.map { it.id }, view.targets.map { it.id })
        assertTrue(view.targets.none { it.active })
        assertTrue(view.presets.map { it.id }.containsAll(listOf("latency", "bandwidth", "timeout", "reset", "slow_close", "limit_data", "outage")))
    }

    @Test
    fun `first use creates the proxy towards the host and adds a downstream toxic with defaults`() = runBlocking {
        val tox = FakeToxiproxy()
        val result = Chaos(tox).apply("floci-aws", "latency", emptyMap())
        val proxy = tox.proxies.getValue("floci-aws")
        assertEquals("0.0.0.0:14566", proxy.listen)
        assertEquals("host.docker.internal:4566", proxy.upstream)
        val toxic = proxy.toxics.single()
        assertEquals("latency", toxic["type"]!!.jsonPrimitive.content)
        assertEquals("downstream", toxic["stream"]!!.jsonPrimitive.content)
        assertEquals("1.0", toxic["toxicity"]!!.jsonPrimitive.content)
        assertEquals("500", toxic["attributes"]!!.jsonObject["latency"]!!.jsonPrimitive.content)
        assertEquals("100", toxic["attributes"]!!.jsonObject["jitter"]!!.jsonPrimitive.content)
        assertTrue(result.active && result.enabled)
        assertEquals("http://localhost:14566", result.endpoint)
        assertEquals("latency", result.toxics.single().name)
    }

    @Test
    fun `custom values and toxicity percentage are sent`() = runBlocking {
        val tox = FakeToxiproxy()
        Chaos(tox).apply("redis", "latency", mapOf("latency" to 1200.0, "jitter" to 0.0, "toxicity" to 30.0))
        val toxic = tox.proxies.getValue("redis").toxics.single()
        assertEquals("1200", toxic["attributes"]!!.jsonObject["latency"]!!.jsonPrimitive.content)
        assertEquals("0.3", toxic["toxicity"]!!.jsonPrimitive.content)
        assertEquals("host.docker.internal:6379", tox.proxies.getValue("redis").upstream)
    }

    @Test
    fun `applying the same preset twice replaces the toxic instead of stacking it`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        chaos.apply("minio", "latency", mapOf("latency" to 100.0))
        val result = chaos.apply("minio", "latency", mapOf("latency" to 900.0))
        assertEquals(1, tox.proxies.getValue("minio").toxics.size)
        assertEquals("900", result.toxics.single().attributes["latency"]!!.jsonPrimitive.content)
    }

    @Test
    fun `different presets stack`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        chaos.apply("postgres", "latency", emptyMap())
        val result = chaos.apply("postgres", "bandwidth", mapOf("rate" to 5.0))
        assertEquals(listOf("latency", "bandwidth"), result.toxics.map { it.name })
        assertEquals("bandwidth", tox.proxies.getValue("postgres").toxics.last()["type"]!!.jsonPrimitive.content)
    }

    @Test
    fun `reset preset maps to reset_peer`() = runBlocking {
        val tox = FakeToxiproxy()
        Chaos(tox).apply("floci-gcp", "reset", emptyMap())
        assertEquals("reset_peer", tox.proxies.getValue("floci-gcp").toxics.single()["type"]!!.jsonPrimitive.content)
    }

    @Test
    fun `outage disables the proxy without a toxic and clear brings it back`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        val down = chaos.apply("floci-aws", "outage", emptyMap())
        assertFalse(down.enabled)
        assertTrue(tox.proxies.getValue("floci-aws").toxics.isEmpty())
        val back = chaos.clear("floci-aws")
        assertTrue(back.enabled)
    }

    @Test
    fun `a toxic preset re-enables a proxy that was in outage`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        chaos.apply("floci-aws", "outage", emptyMap())
        val result = chaos.apply("floci-aws", "latency", emptyMap())
        assertTrue(result.enabled)
    }

    @Test
    fun `clear removes every toxic and is a no-op for a target that was never proxied`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        chaos.apply("redis", "latency", emptyMap())
        chaos.apply("redis", "bandwidth", emptyMap())
        val cleared = chaos.clear("redis")
        assertTrue(cleared.toxics.isEmpty() && cleared.enabled)

        val before = tox.writes().size
        val untouched = chaos.clear("floci-oci")
        assertFalse(untouched.active)
        assertEquals(before, tox.writes().size, "no proxy must be created just to clear it")
    }

    @Test
    fun `removing one toxic leaves the others`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        chaos.apply("redis", "latency", emptyMap())
        chaos.apply("redis", "bandwidth", emptyMap())
        val result = chaos.removeToxic("redis", "latency")
        assertEquals(listOf("bandwidth"), result.toxics.map { it.name })
        assertFailsWith<InvalidParam> { runBlocking { chaos.removeToxic("redis", "../x") } }
        assertFailsWith<EmulatorError> { runBlocking { chaos.removeToxic("redis", "ghost") } }
    }

    @Test
    fun `out of range or non numeric values are rejected before any write`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        assertFailsWith<InvalidParam> { runBlocking { chaos.apply("redis", "latency", mapOf("latency" to -1.0)) } }
        assertFailsWith<InvalidParam> { runBlocking { chaos.apply("redis", "latency", mapOf("latency" to 99_999_999.0)) } }
        assertFailsWith<InvalidParam> { runBlocking { chaos.apply("redis", "latency", mapOf("toxicity" to 0.0)) } }
        assertFailsWith<InvalidParam> { runBlocking { chaos.apply("redis", "latency", mapOf("toxicity" to 101.0)) } }
        assertFailsWith<InvalidParam> { runBlocking { chaos.apply("redis", "latency", mapOf("latency" to Double.NaN)) } }
        assertFailsWith<InvalidParam> { runBlocking { chaos.apply("redis", "nope", emptyMap()) } }
        assertFailsWith<UnknownTarget> { runBlocking { chaos.apply("../admin", "latency", emptyMap()) } }
        assertTrue(tox.writes().isEmpty(), tox.writes().toString())
    }

    @Test
    fun `reset all clears everything`() = runBlocking {
        val tox = FakeToxiproxy()
        val chaos = Chaos(tox)
        chaos.apply("redis", "latency", emptyMap())
        chaos.apply("minio", "outage", emptyMap())
        val view = chaos.resetAll()
        assertTrue(view.targets.filter { it.active }.all { it.enabled && it.toxics.isEmpty() })
        assertContains(tox.calls, "POST /reset")
    }

    @Test
    fun `proxy ports are unique and never collide with emulator ports`() {
        val proxy = ChaosTargets.all.map { it.proxyPort }
        val upstream = ChaosTargets.all.map { it.upstreamPort }
        assertEquals(proxy.size, proxy.toSet().size)
        assertTrue(proxy.intersect(upstream.toSet()).isEmpty())
        assertFalse(ChaosTargets.API_PORT in proxy)
    }

    @Test
    fun `every target points at a real provider port`() {
        val ports = Catalog.providers.associate { it.id to it.ports.toSet() }
        ChaosTargets.all.forEach { t ->
            val owner = t.id.substringBefore("-blob").substringBefore("-queue").substringBefore("-table")
            assertTrue(t.upstreamPort in ports.getValue(owner), "${t.id} -> $owner:${t.upstreamPort}")
        }
    }
}

class ChaosCatalogTest {
    @Test
    fun `toxiproxy publishes its api and every proxy port`() {
        val tox = Catalog.find("toxiproxy")!!
        assertTrue(ChaosTargets.API_PORT in tox.ports)
        assertTrue(tox.ports.containsAll(ChaosTargets.all.map { it.proxyPort }))
    }

    @Test
    fun `toxiproxy run args add the host alias before the image`() {
        val args = CliContainerRuntime.buildRunArgs(Catalog.find("toxiproxy")!!, emptyMap())
        assertTrue(args.indexOf("--add-host=host.docker.internal:host-gateway") in 0 until args.indexOf("ghcr.io/shopify/toxiproxy"))
        assertContains(args, "127.0.0.1:8474:8474")
    }
}

class ChaosExportsTest {
    @Test
    fun `env through chaos rewrites only emulators that have a proxy`() {
        val defs = listOf("floci-aws", "redis", "redpanda", "minio").map { Catalog.find(it)!! }
        val plain = Exports.env(defs)
        val chaos = Exports.env(defs, chaos = true)
        assertContains(plain, "AWS_ENDPOINT_URL=http://localhost:4566")
        assertContains(chaos, "AWS_ENDPOINT_URL=http://localhost:14566")
        assertContains(chaos, "REDIS_URL=redis://localhost:16379")
        assertContains(chaos, "MINIO_ENDPOINT=http://localhost:19000")
        assertContains(chaos, "KAFKA_BOOTSTRAP_SERVERS=localhost:9092", message = "redpanda has no proxy")
        assertContains(chaos, "Toxiproxy")
        assertFalse(plain.contains("14566"))
    }

    @Test
    fun `tofu and aws cli exports can target the proxy`() {
        assertContains(Exports.tofuAwsOverride(chaos = true), "\"http://localhost:14566\"")
        assertFalse(Exports.tofuAwsOverride().contains("14566"))
        assertContains(Exports.awsCliProfile(chaos = true), "endpoint_url = http://localhost:14566")
        assertContains(Exports.awsCliProfile(), "endpoint_url = http://localhost:4566")
    }
}

class ChaosApiTest {
    private fun app(tox: FakeToxiproxy, token: String? = null, block: suspend io.ktor.server.testing.ApplicationTestBuilder.() -> Unit) = testApplication {
        application { nimboModule(NimboConfig(apiToken = token), FakeRuntime(), chaos = Chaos(tox)) }
        block()
    }

    @Test
    fun `get returns availability presets and targets`() = app(FakeToxiproxy()) {
        val body = Json.parseToJsonElement(client.get("/api/chaos").bodyAsText()).jsonObject
        assertEquals("true", body["available"]!!.jsonPrimitive.content)
        assertTrue((body["presets"] as JsonArray).size >= 7)
        assertEquals(ChaosTargets.all.size, (body["targets"] as JsonArray).size)
    }

    @Test
    fun `get still answers 200 when toxiproxy is down`() = app(FakeToxiproxy().apply { unreachable = true }) {
        val response = client.get("/api/chaos")
        assertEquals(HttpStatusCode.OK, response.status)
        assertContains(response.bodyAsText(), "\"available\":false")
    }

    @Test
    fun `apply with a json body, then delete`() {
        val tox = FakeToxiproxy()
        app(tox) {
            val applied = client.post("/api/chaos/targets/redis/latency") {
                contentType(ContentType.Application.Json)
                setBody("""{"params":{"latency":250,"jitter":10}}""")
            }
            assertEquals(HttpStatusCode.OK, applied.status)
            assertContains(applied.bodyAsText(), "\"name\":\"latency\"")
            assertEquals("250", tox.proxies.getValue("redis").toxics.single()["attributes"]!!.jsonObject["latency"]!!.jsonPrimitive.content)

            assertEquals(HttpStatusCode.OK, client.delete("/api/chaos/targets/redis/toxics/latency").status)
            assertTrue(tox.proxies.getValue("redis").toxics.isEmpty())
        }
    }

    @Test
    fun `apply without a body uses defaults`() {
        val tox = FakeToxiproxy()
        app(tox) {
            assertEquals(HttpStatusCode.OK, client.post("/api/chaos/targets/redis/outage").status)
            assertFalse(tox.proxies.getValue("redis").enabled)
        }
    }

    @Test
    fun `bad input is 400 and unknown target is 404`() = app(FakeToxiproxy()) {
        assertEquals(HttpStatusCode.BadRequest, client.post("/api/chaos/targets/redis/nope").status)
        val tooBig = client.post("/api/chaos/targets/redis/latency") {
            contentType(ContentType.Application.Json)
            setBody("""{"params":{"latency":9999999}}""")
        }
        assertEquals(HttpStatusCode.BadRequest, tooBig.status)
        assertEquals(HttpStatusCode.NotFound, client.post("/api/chaos/targets/ghost/latency").status)
        assertEquals(HttpStatusCode.NotFound, client.delete("/api/chaos/targets/ghost/toxics").status)
    }

    @Test
    fun `writing while toxiproxy is down is a 503`() = app(FakeToxiproxy().apply { unreachable = true }) {
        assertEquals(HttpStatusCode.ServiceUnavailable, client.post("/api/chaos/targets/redis/latency").status)
        assertEquals(HttpStatusCode.ServiceUnavailable, client.post("/api/chaos/reset").status)
    }

    @Test
    fun `stack export can go through chaos`() = app(FakeToxiproxy()) {
        val plain = client.get("/api/stacks/aws-basic/export?format=env").bodyAsText()
        val chaos = client.get("/api/stacks/aws-basic/export?format=env&chaos=true").bodyAsText()
        assertContains(plain, "localhost:4566")
        assertContains(chaos, "localhost:14566")
    }

    @Test
    fun `chaos is behind the api token`() = app(FakeToxiproxy(), token = "t") {
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/chaos").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/chaos/reset").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/chaos/targets/redis/outage").status)
    }
}
