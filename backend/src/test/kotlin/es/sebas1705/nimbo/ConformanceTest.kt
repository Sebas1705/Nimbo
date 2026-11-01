package es.sebas1705.nimbo

import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpStatusCode
import io.ktor.server.testing.testApplication
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.ByteArrayInputStream
import java.util.Base64
import java.util.zip.ZipInputStream
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** Minimal in-memory S3, enough for the S3 suite. [failOn] makes matching requests return 500. */
private class FakeS3(var failOn: (String, String) -> Boolean = { _, _ -> false }) : HttpGateway {
    val buckets = linkedMapOf<String, MutableMap<String, String>>()
    val calls = mutableListOf<String>()

    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply {
        val path = url.removePrefix("http://localhost:4566")
        calls += "$method $path"
        if (failOn(method, path)) return HttpReply(500, "boom")
        val clean = path.substringBefore('?')
        val parts = clean.trim('/').split('/', limit = 2)
        val bucket = parts[0]
        val key = parts.getOrNull(1)
        return when {
            method == "PUT" && key == null -> { buckets[bucket] = mutableMapOf(); HttpReply(200, "") }
            method == "PUT" -> buckets[bucket]?.let { it[key!!] = body.orEmpty(); HttpReply(200, "") } ?: HttpReply(404, "NoSuchBucket")
            method == "GET" && key == null ->
                buckets[bucket]?.let { HttpReply(200, it.keys.joinToString("") { k -> "<Contents><Key>$k</Key></Contents>" }) } ?: HttpReply(404, "NoSuchBucket")
            method == "GET" -> buckets[bucket]?.get(key!!)?.let { HttpReply(200, it) } ?: HttpReply(404, "NoSuchKey")
            method == "DELETE" && key != null -> { buckets[bucket]?.remove(key); HttpReply(204, "") }
            method == "DELETE" -> { buckets.remove(bucket); HttpReply(204, "") }
            else -> HttpReply(400, "unsupported")
        }
    }
}

class ScenarioTest {
    @Test
    fun `passing steps are recorded with timings`() = runBlocking {
        val s = Scenario()
        s.step("a") { }
        s.step("b") { }
        assertEquals(listOf("pass", "pass"), s.steps.map { it.status })
        assertTrue(s.steps.all { it.ms >= 0 })
    }

    @Test
    fun `a failure skips the dependent steps that follow`() = runBlocking {
        val s = Scenario()
        s.step("create") { }
        s.step("use") { throw StepFailed("nope") }
        s.step("later") { error("must not run") }
        assertEquals(listOf("pass", "fail", "skipped"), s.steps.map { it.status })
        assertEquals("nope", s.steps[1].detail)
        assertContains(s.steps[2].detail!!, "earlier step")
    }

    @Test
    fun `independent steps still run after a failure and do not poison the rest`() = runBlocking {
        val s = Scenario()
        s.step("create") { throw StepFailed("x") }
        var ran = false
        s.step("optional", independent = true) { ran = true }
        s.step("dependent") { }
        assertTrue(ran)
        assertEquals(listOf("fail", "pass", "skipped"), s.steps.map { it.status })
    }

    @Test
    fun `a failing independent step does not skip the next dependent step`() = runBlocking {
        val s = Scenario()
        s.step("create") { }
        s.step("optional", independent = true) { throw StepFailed("meh") }
        s.step("after") { }
        assertEquals(listOf("pass", "fail", "pass"), s.steps.map { it.status })
    }

    @Test
    fun `unexpected exceptions become failures instead of crashing the run`() = runBlocking {
        val s = Scenario()
        s.step("boom") { error("kaput") }
        assertEquals("fail", s.steps.single().status)
        assertContains(s.steps.single().detail!!, "kaput")
    }

    @Test
    fun `an unreachable emulator aborts the suite`() {
        assertFailsWith<EmulatorUnreachable> { runBlocking { Scenario().step("x") { throw EmulatorUnreachable("down") } } }
    }

    @Test
    fun `cleanups run in reverse order and survive their own failures`() = runBlocking {
        val order = mutableListOf<Int>()
        val c = Cleanups()
        c.add { order += 1 }
        c.add { error("cleanup broke") }
        c.add { order += 3 }
        c.run()
        assertEquals(listOf(3, 1), order)
    }
}

class ConformanceTest {
    private fun s3Only(gw: HttpGateway) = Conformance(gw)

    @Test
    fun `a faithful emulator gets a full status`() = runBlocking {
        val fake = FakeS3()
        val suite = s3Only(fake).run(suite = "s3").suites.single()
        assertEquals("full", suite.status)
        assertEquals(6, suite.steps.size)
        assertTrue(fake.buckets.isEmpty(), "the suite must leave nothing behind")
    }

    @Test
    fun `a failure midway is partial, later steps are skipped and cleanup still runs`() = runBlocking {
        val fake = FakeS3(failOn = { m, p -> m == "GET" && p.contains("/hello.txt") })
        val suite = s3Only(fake).run(suite = "s3").suites.single()
        assertEquals("partial", suite.status)
        assertEquals(listOf("pass", "pass", "fail", "skipped", "skipped", "skipped"), suite.steps.map { it.status })
        assertContains(suite.steps[2].detail!!, "HTTP 500")
        assertTrue(fake.buckets.isEmpty(), "cleanup must remove the bucket even after a failure")
        assertTrue(fake.calls.any { it.startsWith("DELETE") })
    }

    @Test
    fun `an emulator that implements nothing is missing`() = runBlocking {
        val gw = HttpGateway { _, _, _, _ -> HttpReply(404, "nope") }
        val suite = s3Only(gw).run(suite = "s3").suites.single()
        assertEquals("missing", suite.status)
        assertEquals("fail", suite.steps.first().status)
        assertTrue(suite.steps.drop(1).all { it.status == "skipped" })
    }

    @Test
    fun `an unreachable emulator is offline and nothing is attempted afterwards`() = runBlocking {
        var calls = 0
        val gw = HttpGateway { _, _, _, _ -> calls++; throw EmulatorUnreachable("down") }
        val suite = s3Only(gw).run(suite = "s3").suites.single()
        assertEquals("offline", suite.status)
        assertEquals(1, calls, "no cleanup traffic towards a dead emulator")
    }

    @Test
    fun `cloud and suite filters narrow the run`() = runBlocking {
        val gw = HttpGateway { _, _, _, _ -> HttpReply(404, "") }
        val conformance = Conformance(gw)
        val aws = conformance.run(cloud = "aws").suites
        assertTrue(aws.isNotEmpty() && aws.all { it.cloud == "aws" })
        assertEquals(listOf("pubsub"), conformance.run(cloud = "gcp", suite = "pubsub").suites.map { it.id })
        assertTrue(conformance.run(cloud = "aws", suite = "pubsub").suites.isEmpty())
    }

    @Test
    fun `the catalog covers three clouds with unique suite ids`() {
        val catalog = Conformance(HttpGateway { _, _, _, _ -> HttpReply(200, "") }).catalog
        assertEquals(setOf("aws", "azure", "gcp"), catalog.map { it.cloud }.toSet())
        assertEquals(catalog.size, catalog.map { it.id }.toSet().size)
        assertTrue(catalog.size >= 15)
    }

    @Test
    fun `resources are named per run so two runs never collide`() = runBlocking {
        val first = FakeS3().also { s3Only(it).run(suite = "s3") }.calls.first { it.startsWith("PUT") }
        val second = FakeS3().also { s3Only(it).run(suite = "s3") }.calls.first { it.startsWith("PUT") }
        assertContains(first, "nimbo-fit-")
        assertTrue(first != second)
    }

    @Test
    fun `the lambda suite uploads a real zip with the handler`() = runBlocking {
        var zip: String? = null
        val gw = HttpGateway { _, url, _, body ->
            if (url.endsWith("/2015-03-31/functions") && body != null && "ZipFile" in body) {
                zip = Json.parseToJsonElement(body).jsonObject["Code"]!!.jsonObject["ZipFile"]!!.jsonPrimitive.content
            }
            HttpReply(200, """{"ok":true}""")
        }
        val result = Conformance(gw).run(suite = "lambda").suites.single()
        assertEquals("full", result.status)
        val entries = ZipInputStream(ByteArrayInputStream(Base64.getDecoder().decode(zip!!))).use { z ->
            generateSequence { z.nextEntry }.associate { it.name to z.readBytes().decodeToString() }
        }
        assertContains(entries.getValue("index.js"), "exports.handler")
    }
}

class FidelityApiTest {
    private val faithful = Conformance(FakeS3())

    @Test
    fun `state starts empty with the full catalog`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), conformance = faithful) }
        val body = Json.parseToJsonElement(client.get("/api/fidelity").bodyAsText()).jsonObject
        assertEquals(0, body["results"]!!.jsonArray.size)
        assertTrue(body["catalog"]!!.jsonArray.size >= 15)
        assertEquals("false", body["running"]!!.jsonPrimitive.content)
    }

    @Test
    fun `a run stores results and a later run of another suite keeps them`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), conformance = faithful) }
        client.post("/api/fidelity/run?cloud=aws&suite=s3")
        val after = Json.parseToJsonElement(client.post("/api/fidelity/run?cloud=gcp&suite=pubsub").bodyAsText()).jsonObject
        val ids = after["results"]!!.jsonArray.map { it.jsonObject["id"]!!.jsonPrimitive.content }
        assertEquals(listOf("s3", "pubsub"), ids)
        assertContains(after["summary"].toString(), "full")
    }

    @Test
    fun `invalid filters are rejected before any traffic`() = testApplication {
        val fake = FakeS3()
        application { nimboModule(NimboConfig(), FakeRuntime(), conformance = Conformance(fake)) }
        assertEquals(HttpStatusCode.BadRequest, client.post("/api/fidelity/run?cloud=oracle").status)
        assertEquals(HttpStatusCode.BadRequest, client.post("/api/fidelity/run?suite=nope").status)
        assertEquals(HttpStatusCode.BadRequest, client.post("/api/fidelity/run?cloud=gcp&suite=s3").status)
        assertTrue(fake.calls.isEmpty())
    }

    @Test
    fun `only one run at a time`() = testApplication {
        val store = FidelityStore()
        application { nimboModule(NimboConfig(), FakeRuntime(), conformance = faithful, fidelity = store) }
        assertTrue(store.lock.tryLock())
        assertEquals(HttpStatusCode.Conflict, client.post("/api/fidelity/run").status)
        assertContains(client.get("/api/fidelity").bodyAsText(), "\"running\":true")
        store.lock.unlock()
        assertEquals(HttpStatusCode.OK, client.post("/api/fidelity/run?suite=s3").status)
    }

    @Test
    fun `fidelity is behind the api token`() = testApplication {
        application { nimboModule(NimboConfig(apiToken = "t"), FakeRuntime(), conformance = faithful) }
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/fidelity").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/fidelity/run").status)
    }
}
