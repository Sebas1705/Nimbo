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
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val TRACE_ID = "5b8efff798038103d269b633813fc60c"

private fun span(id: String, op: String, start: Long, dur: Long, parent: String? = null, error: Boolean = false, proc: String = "p1") = buildString {
    append("""{"traceID":"$TRACE_ID","spanID":"$id","operationName":"$op","startTime":$start,"duration":$dur,"processID":"$proc",""")
    append(""""references":[${parent?.let { """{"refType":"CHILD_OF","traceID":"$TRACE_ID","spanID":"$it"}""" }.orEmpty()}],""")
    append(""""tags":[{"key":"otel.status_code","type":"string","value":"${if (error) "ERROR" else "OK"}"}${if (error) """,{"key":"error","type":"bool","value":true}""" else ""}]}""")
}

private fun trace(vararg spans: String, processes: String = """{"p1":{"serviceName":"checkout","tags":[]},"p2":{"serviceName":"billing","tags":[]}}""") =
    """{"traceID":"$TRACE_ID","spans":[${spans.joinToString(",")}],"processes":$processes}"""

internal fun envelope(vararg traces: String) = """{"data":[${traces.joinToString(",")}],"total":0,"limit":0,"offset":0,"errors":null}"""

internal class FakeJaeger(var responses: (String, String) -> HttpReply = { _, _ -> HttpReply(404, "") }) : HttpGateway {
    val calls = mutableListOf<Triple<String, String, String?>>()
    var unreachable = false
    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply {
        if (unreachable) throw EmulatorUnreachable("down")
        calls += Triple(method, url, body)
        return responses(method, url)
    }
}

class TracesTest {
    private val root = span("aaaa000000000001", "POST /checkout", 1_000_000, 120_000)

    @Test
    fun `services are listed sorted`() = runBlocking {
        val gw = FakeJaeger { _, _ -> HttpReply(200, """{"data":["zeta","checkout","jaeger-all-in-one"],"total":3}""") }
        assertEquals(listOf("checkout", "jaeger-all-in-one", "zeta"), Traces(gw).services())
        assertEquals("http://localhost:16686/api/services", gw.calls.single().second)
    }

    @Test
    fun `an empty jaeger answers with no services instead of failing`() = runBlocking {
        assertTrue(Traces(FakeJaeger { _, _ -> HttpReply(200, """{"data":null,"total":0}""") }).services().isEmpty())
    }

    @Test
    fun `summary finds the root, counts errors and measures the whole trace`() = runBlocking {
        val body = envelope(
            trace(
                span("aaaa000000000002", "SQS SendMessage", 1_010_000, 50_000, parent = "aaaa000000000001", proc = "p2"),
                span("aaaa000000000003", "DynamoDB PutItem", 1_065_000, 45_000, parent = "aaaa000000000001", error = true),
                root,
            ),
        )
        val gw = FakeJaeger { _, _ -> HttpReply(200, body) }
        val s = Traces(gw).list("checkout").single()
        assertEquals("checkout", s.service)
        assertEquals("POST /checkout", s.operation)
        assertEquals(3, s.spans)
        assertEquals(1, s.errors)
        assertEquals(120_000, s.durationMicros)
        assertEquals(1_000, s.startedAt, "microseconds to milliseconds")
        assertContains(gw.calls.single().second, "service=checkout")
        assertContains(gw.calls.single().second, "limit=20")
    }

    @Test
    fun `traces come newest first and the limit is clamped`() = runBlocking {
        fun t(id: String, start: Long) = trace(span(id, "op", start, 10)).replace(TRACE_ID, "ab".repeat(8) + id.padEnd(16, '0'))
        val gw = FakeJaeger { _, _ -> HttpReply(200, envelope(t("0001", 1_000), t("0002", 9_000), t("0003", 5_000))) }
        val list = Traces(gw).list("svc", limit = 100_000)
        assertEquals(listOf(9L, 5L, 1L), list.map { it.startedAt })
        assertContains(gw.calls.single().second, "limit=100")
    }

    @Test
    fun `detail orders spans depth first and computes offsets`() = runBlocking {
        val body = envelope(
            trace(
                span("aaaa000000000004", "grandchild", 1_020_000, 5_000, parent = "aaaa000000000002"),
                span("aaaa000000000003", "second child", 1_070_000, 30_000, parent = "aaaa000000000001", error = true),
                span("aaaa000000000002", "first child", 1_010_000, 50_000, parent = "aaaa000000000001", proc = "p2"),
                root,
            ),
        )
        val d = Traces(FakeJaeger { _, _ -> HttpReply(200, body) }).detail(TRACE_ID)
        assertEquals(listOf("POST /checkout", "first child", "grandchild", "second child"), d.spans.map { it.operation })
        assertEquals(listOf(0, 1, 2, 1), d.spans.map { it.depth })
        assertEquals(listOf(0L, 10_000L, 20_000L, 70_000L), d.spans.map { it.offsetMicros })
        assertEquals("billing", d.spans[1].service)
        assertEquals(listOf(false, false, false, true), d.spans.map { it.error })
        assertEquals(120_000, d.durationMicros)
        assertEquals(null, d.spans.first().parentId)
    }

    @Test
    fun `orphans and cycles do not hang or drop spans`() = runBlocking {
        val orphan = span("bbbb000000000001", "orphan", 1_005_000, 5_000, parent = "ffffffffffffffff")
        val a = span("cccc000000000001", "a", 1_001_000, 1_000, parent = "cccc000000000002")
        val b = span("cccc000000000002", "b", 1_002_000, 1_000, parent = "cccc000000000001")
        val d = Traces(FakeJaeger { _, _ -> HttpReply(200, envelope(trace(root, orphan, a, b))) }).detail(TRACE_ID)
        assertEquals(4, d.spans.size)
        assertEquals(setOf("POST /checkout", "orphan", "a", "b"), d.spans.map { it.operation }.toSet())
        assertEquals(d.spans.size, d.spans.map { it.id }.toSet().size)
        assertEquals(0, d.spans.first { it.operation == "orphan" }.depth)
        assertEquals(null, d.spans.first { it.operation == "orphan" }.parentId, "a parent we do not have is not shown")
    }

    @Test
    fun `unknown trace is a 404 style error`() {
        val ex = assertFailsWith<EmulatorError> { runBlocking { Traces(FakeJaeger { _, _ -> HttpReply(200, """{"data":[]}""") }).detail(TRACE_ID) } }
        assertEquals(404, ex.status)
    }

    @Test
    fun `invalid input never reaches jaeger`() {
        val gw = FakeJaeger { _, _ -> HttpReply(200, "{}") }
        val traces = Traces(gw)
        listOf("", "a b", "x/y", "../z", "a&b=c", "s".repeat(200)).forEach { bad -> assertFailsWith<InvalidName>(bad) { runBlocking { traces.list(bad) } } }
        listOf("", "xyz", "../1", "ABCDEF0123456789", "1".repeat(40), "abc").forEach { bad -> assertFailsWith<InvalidName>(bad) { runBlocking { traces.detail(bad) } } }
        assertTrue(gw.calls.isEmpty())
    }

    @Test
    fun `service names are url encoded`() = runBlocking {
        val gw = FakeJaeger { _, _ -> HttpReply(200, envelope()) }
        Traces(gw).list("my.service-1_a")
        assertContains(gw.calls.single().second, "service=my.service-1_a")
    }

    @Test
    fun `an unreachable jaeger and a failing one are different errors`() {
        val down = FakeJaeger().apply { unreachable = true }
        assertFailsWith<EmulatorUnreachable> { runBlocking { Traces(down).services() } }
        assertFailsWith<EmulatorError> { runBlocking { Traces(FakeJaeger { _, _ -> HttpReply(500, "x") }).services() } }
    }

    @Test
    fun `the sample trace is valid otlp with a parent child structure`() = runBlocking {
        val gw = FakeJaeger { _, _ -> HttpReply(200, "{}") }
        val sample = Traces(gw).sample()
        assertEquals("nimbo-demo", sample.service)
        assertTrue(Regex("[0-9a-f]{32}").matches(sample.traceId))
        val call = gw.calls.single()
        assertEquals("POST", call.first)
        assertEquals("http://localhost:4318/v1/traces", call.second)
        val doc = Json.parseToJsonElement(call.third!!).jsonObject
        val spans = doc["resourceSpans"]!!.jsonArray[0].jsonObject["scopeSpans"]!!.jsonArray[0].jsonObject["spans"]!!.jsonArray.map { it.jsonObject }
        assertEquals(3, spans.size)
        assertTrue(spans.all { it["traceId"]!!.jsonPrimitive.content == sample.traceId })
        val rootSpan = spans.single { it["parentSpanId"] == null }
        assertTrue(spans.filter { it !== rootSpan }.all { it["parentSpanId"]!!.jsonPrimitive.content == rootSpan["spanId"]!!.jsonPrimitive.content })
        assertTrue(spans.all { Regex("[0-9a-f]{16}").matches(it["spanId"]!!.jsonPrimitive.content) })
        assertTrue(spans.all { it["status"]!!.jsonObject["code"]!!.jsonPrimitive.content == "1" })
        assertTrue(spans.all { it["endTimeUnixNano"]!!.jsonPrimitive.content.toLong() > it["startTimeUnixNano"]!!.jsonPrimitive.content.toLong() })
    }

    @Test
    fun `the failing sample marks exactly one span as an error, and ids differ between runs`() = runBlocking {
        val gw = FakeJaeger { _, _ -> HttpReply(200, "{}") }
        val traces = Traces(gw)
        val first = traces.sample(failing = true)
        val second = traces.sample()
        assertTrue(first.traceId != second.traceId)
        val spans = Json.parseToJsonElement(gw.calls.first().third!!).jsonObject["resourceSpans"]!!.jsonArray[0].jsonObject["scopeSpans"]!!.jsonArray[0].jsonObject["spans"]!!.jsonArray
        assertEquals(1, spans.count { it.jsonObject["status"]!!.jsonObject["code"]!!.jsonPrimitive.content == "2" })
    }

    @Test
    fun `a rejected otlp intake is reported`() {
        assertFailsWith<EmulatorError> { runBlocking { Traces(FakeJaeger { _, _ -> HttpReply(400, "bad") }).sample() } }
    }
}

class TraceApiTest {
    private val body = envelope(trace(root(), span("aaaa000000000002", "child", 1_010_000, 10_000, parent = "aaaa000000000001")))
    private fun root() = span("aaaa000000000001", "GET /", 1_000_000, 100_000)

    private fun gateway() = FakeJaeger { m, url ->
        when {
            url.endsWith("/api/services") -> HttpReply(200, """{"data":["checkout"]}""")
            "/api/traces?service" in url -> HttpReply(200, body)
            url.endsWith("/api/traces/$TRACE_ID") -> HttpReply(200, body)
            m == "POST" -> HttpReply(200, "{}")
            else -> HttpReply(404, "")
        }
    }

    @Test
    fun `list, detail and sample over http`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), traces = Traces(gateway())) }
        assertContains(client.get("/api/traces/services").bodyAsText(), "checkout")
        assertContains(client.get("/api/traces?service=checkout").bodyAsText(), "\"spans\":2")
        val detail = client.get("/api/traces/$TRACE_ID").bodyAsText()
        assertContains(detail, "\"depth\":1")
        val sample = client.post("/api/traces/sample?error=true")
        assertEquals(HttpStatusCode.OK, sample.status)
        assertContains(sample.bodyAsText(), "nimbo-demo")
    }

    @Test
    fun `validation and emulator errors map to http statuses`() = testApplication {
        val down = FakeJaeger().apply { unreachable = true }
        application { nimboModule(NimboConfig(), FakeRuntime(), traces = Traces(down)) }
        assertEquals(HttpStatusCode.ServiceUnavailable, client.get("/api/traces/services").status)
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/traces").status)
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/traces?service=a%20b").status)
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/traces/nothex").status)
        assertFalse(down.calls.isNotEmpty(), "bad requests never reach jaeger")
    }

    @Test
    fun `an unknown trace is a 404`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), traces = Traces(FakeJaeger { _, _ -> HttpReply(200, """{"data":[]}""") })) }
        assertEquals(HttpStatusCode.NotFound, client.get("/api/traces/$TRACE_ID").status)
    }

    @Test
    fun `traces are behind the api token`() = testApplication {
        application { nimboModule(NimboConfig(apiToken = "t"), FakeRuntime(), traces = Traces(gateway())) }
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/traces/services").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/traces/sample").status)
    }
}
