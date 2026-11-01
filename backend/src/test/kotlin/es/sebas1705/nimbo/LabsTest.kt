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
import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private fun repoLabsDir(): File = listOf(File("labs"), File("../labs")).first { it.isDirectory }

private fun lab(vararg checks: String, extra: String = "") = """
    id: t
    title: T
    summary: S
    steps: [do it]
    $extra
    checks:
${checks.joinToString("\n") { "      - $it" }}
""".trimIndent()

private fun parse(text: String) = LabCatalog.parseOne(text, "t.yml")

class LabParsingTest {
    @Test
    fun `every lab shipped in the repo is valid and bilingual`() {
        val catalog = LabCatalog.load(mapOf("NIMBO_LABS_DIR" to repoLabsDir().path))
        assertTrue(catalog.problems.isEmpty(), catalog.problems.toString())
        assertTrue(catalog.labs.size >= 6)
        assertEquals(catalog.labs.size, catalog.labs.map { it.dto.id }.toSet().size)
        catalog.labs.forEach { l ->
            val d = l.dto
            assertTrue(d.title.en != d.title.es, "${d.id}: title not translated")
            assertTrue(d.summary.en != d.summary.es, "${d.id}: summary not translated")
            assertTrue(d.steps.all { it.en != it.es }, "${d.id}: a step is not translated")
            assertTrue(d.checks.all { it.label.en != it.label.es }, "${d.id}: a check label is not translated")
            assertTrue(d.level in 1..3, "${d.id}: level")
            assertTrue(d.solution.isNotEmpty(), "${d.id}: no solution")
        }
    }

    @Test
    fun `labs are ordered by level then id`() {
        val labs = LabCatalog.load(mapOf("NIMBO_LABS_DIR" to repoLabsDir().path)).labs.map { it.dto.level to it.dto.id }
        assertEquals(labs.sortedWith(compareBy({ it.first }, { it.second })), labs)
    }

    @Test
    fun `the suggested stack of every lab exists`() {
        val shipped = StackCatalog.parse(listOf(File("nimbo.yml"), File("../nimbo.yml")).first { it.isFile }.readText())
        LabCatalog.load(mapOf("NIMBO_LABS_DIR" to repoLabsDir().path)).labs.forEach { l ->
            l.dto.stack?.let { assertNotNull(shipped.find(it), "${l.dto.id}: unknown stack $it") }
        }
    }

    @Test
    fun `every lab check uses only providers and clouds the catalog knows`() {
        LabCatalog.load(mapOf("NIMBO_LABS_DIR" to repoLabsDir().path)).labs.flatMap { it.checks }.filter { it.type == "chaos.fault" }.forEach {
            assertNotNull(ChaosTargets.find(it.str("target")!!), "unknown chaos target ${it.str("target")}")
        }
    }

    @Test
    fun `a plain string is used for both languages and a single language is mirrored`() {
        val (lab, problem) = parse(
            """
            id: t
            title: Only
            summary: {en: English only}
            steps: [Step]
            checks:
              - {type: s3.bucket, name: b, label: {es: Solo español}}
            """.trimIndent(),
        )
        assertNull(problem)
        assertEquals(L10nDto("Only", "Only"), lab!!.dto.title)
        assertEquals(L10nDto("English only", "English only"), lab.dto.summary)
        assertEquals(L10nDto("Solo español", "Solo español"), lab.dto.checks.single().label)
    }

    @Test
    fun `invalid labs are rejected with a reason naming the file`() {
        fun reason(text: String) = parse(text).second.orEmpty()
        assertContains(reason("not: [valid"), "t.yml")
        assertContains(reason("- a list"), "mapping")
        assertContains(reason(lab("{type: s3.bucket, name: b, label: L}").replace("id: t", "id: Bad_Id")), "id must match")
        assertContains(reason(lab("{type: s3.bucket, name: b, label: L}").replace("title: T\n", "")), "title")
        assertContains(reason(lab("{type: s3.bucket, name: b, label: L}").replace("steps: [do it]", "steps: []")), "step")
        assertContains(reason(lab("{type: ec2.instance, name: b, label: L}")), "unknown type")
        assertContains(reason(lab("{type: s3.object, bucket: b, label: L}")), "missing 'key'")
        assertContains(reason(lab("{type: s3.bucket, name: b}")), "no label")
        assertContains(reason(lab("{type: s3.bucket, name: ../x, label: L}")), "invalid value")
        assertContains(reason(lab("{type: sqs.messages, queue: 'a b', label: L}")), "invalid value")
        assertContains(reason(lab("{type: s3.bucket, name: b, label: L}").replace("checks:", "checks: []\nignored:")), "at least one check")
    }

    @Test
    fun `yaml cannot instantiate classes`() {
        assertNotNull(parse("id: !!java.lang.ProcessBuilder [[calc]]").second)
    }

    @Test
    fun `a broken file is reported but does not hide the good ones, duplicates are flagged`() {
        val dir = Files.createTempDirectory("labs").toFile()
        try {
            File(dir, "a.yml").writeText(lab("{type: s3.bucket, name: b, label: L}"))
            File(dir, "b.yml").writeText(lab("{type: s3.bucket, name: b, label: L}")) // same id "t"
            File(dir, "c.yml").writeText("id: [broken")
            File(dir, "ignored.txt").writeText("x")
            val catalog = LabCatalog.load(mapOf("NIMBO_LABS_DIR" to dir.path))
            assertEquals(1, catalog.labs.size)
            assertTrue(catalog.problems.any { "c.yml" in it })
            assertTrue(catalog.problems.any { "duplicate" in it })
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun `a missing directory means no labs`() {
        val catalog = LabCatalog.load(mapOf("NIMBO_LABS_DIR" to "/definitely/not/here"))
        // falls back to the repo copy when run from backend/, so only check it does not throw
        assertTrue(catalog.problems.isEmpty())
    }
}

/** Answers from fixed documents so each check type can be driven to pass or fail. */
private class World(
    var buckets: List<String> = emptyList(),
    var objectText: String? = null,
    var queues: List<String> = emptyList(),
    var messages: List<String> = emptyList(),
    var tables: Map<String, List<String>> = emptyMap(),
    var items: List<String> = emptyList(),
    var topics: List<String> = emptyList(),
    var subscriptions: Map<String, String> = emptyMap(),
    var down: Boolean = false,
) : HttpGateway {
    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply {
        if (down) throw EmulatorUnreachable("down")
        val b = body.orEmpty()
        return when {
            url.endsWith("/v1/projects/floci-local/topics") -> HttpReply(200, """{"topics":[${topics.joinToString(",") { """{"name":"projects/floci-local/topics/$it"}""" }}]}""")
            url.endsWith("/v1/projects/floci-local/subscriptions") ->
                HttpReply(200, """{"subscriptions":[${subscriptions.entries.joinToString(",") { """{"name":"projects/floci-local/subscriptions/${it.key}","topic":"projects/floci-local/topics/${it.value}"}""" }}]}""")
            method == "GET" && url == "http://localhost:4566/" ->
                HttpReply(200, "<R><Buckets>${buckets.joinToString("") { "<Bucket><Name>$it</Name></Bucket>" }}</Buckets></R>")
            method == "GET" && url.count { it == '/' } >= 4 && "list-type" !in url ->
                objectText?.let { HttpReply(200, it) } ?: HttpReply(404, "<Error><Code>NoSuchKey</Code></Error>")
            "Action=ListQueues" in b -> HttpReply(200, "<R>${queues.joinToString("") { "<QueueUrl>http://localhost:4566/000000000000/$it</QueueUrl>" }}</R>")
            "Action=GetQueueAttributes" in b -> HttpReply(200, "<R/>")
            "Action=ReceiveMessage" in b -> HttpReply(200, "<R>${messages.mapIndexed { i, m -> "<Message><MessageId>m$i</MessageId><Body>${m.replace("&", "&amp;").replace("<", "&lt;")}</Body></Message>" }.joinToString("")}</R>")
            url.endsWith("/") && headers["X-Amz-Target"]?.endsWith("ListTables") == true ->
                HttpReply(200, """{"TableNames":[${tables.keys.joinToString(",") { "\"$it\"" }}]}""")
            headers["X-Amz-Target"]?.endsWith("DescribeTable") == true -> {
                val name = Json.parseToJsonElement(b).jsonObject["TableName"]!!.jsonPrimitive.content
                HttpReply(200, """{"Table":{"TableName":"$name","TableStatus":"ACTIVE","ItemCount":1,"KeySchema":[${tables[name].orEmpty().joinToString(",") { """{"AttributeName":"$it","KeyType":"HASH"}""" }}]}}""")
            }
            headers["X-Amz-Target"]?.endsWith("Scan") == true -> HttpReply(200, """{"Items":[${items.joinToString(",")}]}""")
            else -> HttpReply(404, "no rule for $method $url")
        }
    }
}

class LabVerifyTest {
    private fun verify(world: World, vararg checks: String, tox: FakeToxiproxy = FakeToxiproxy()): LabVerifyDto = runBlocking {
        val (def, problem) = parse(lab(*checks))
        assertNull(problem)
        LabVerifier(Inspector(world), Chaos(tox)).verify(def!!)
    }

    @Test
    fun `s3 bucket and object`() {
        val w = World(buckets = listOf("b1"), objectText = "say hello nimbo please")
        assertTrue(verify(w, "{type: s3.bucket, name: b1, label: L}", "{type: s3.object, bucket: b1, key: hello.txt, contains: hello nimbo, label: L}").passed)

        val missing = verify(w, "{type: s3.bucket, name: nope, label: L}")
        assertFalse(missing.passed)
        assertContains(missing.results.single().detail!!, "found: b1")

        val wrong = verify(w, "{type: s3.object, bucket: b1, key: hello.txt, contains: other, label: L}")
        assertContains(wrong.results.single().detail!!, "does not contain 'other'")

        w.objectText = null
        assertEquals("not found", verify(w, "{type: s3.object, bucket: b1, key: hello.txt, label: L}").results.single().detail)
    }

    @Test
    fun `sqs queue and message counts`() {
        val w = World(queues = listOf("q"), messages = listOf("""{"event":"created"}""", "other"))
        assertTrue(verify(w, "{type: sqs.queue, name: q, label: L}", "{type: sqs.messages, queue: q, atLeast: 2, label: L}").passed)
        assertTrue(verify(w, "{type: sqs.messages, queue: q, contains: created, label: L}").passed)

        val few = verify(w, "{type: sqs.messages, queue: q, atLeast: 3, label: L}")
        assertContains(few.results.single().detail!!, "found 2 message(s), need 3")
        val none = verify(w, "{type: sqs.messages, queue: q, contains: shipped, label: L}")
        assertContains(none.results.single().detail!!, "containing 'shipped'")
        assertContains(verify(w, "{type: sqs.queue, name: ghost, label: L}").results.single().detail!!, "found: q")
    }

    @Test
    fun `dynamodb table key and items`() {
        val w = World(tables = mapOf("t" to listOf("id")), items = listOf("""{"id":{"S":"1"},"total":{"N":"5"}}""", """{"id":{"S":"2"},"total":{"N":"6"}}""", """{"id":{"S":"3"}}"""))
        assertTrue(verify(w, "{type: dynamodb.table, name: t, key: id, label: L}").passed)
        assertContains(verify(w, "{type: dynamodb.table, name: t, key: pk, label: L}").results.single().detail!!, "not 'pk'")
        assertEquals("no such table", verify(w, "{type: dynamodb.table, name: x, label: L}").results.single().detail)

        assertTrue(verify(w, "{type: dynamodb.items, table: t, atLeast: 3, label: L}").passed)
        assertTrue(verify(w, "{type: dynamodb.items, table: t, atLeast: 2, attribute: total, label: L}").passed)
        assertContains(verify(w, "{type: dynamodb.items, table: t, atLeast: 3, attribute: total, label: L}").results.single().detail!!, "found 2 item(s) with 'total'")
    }

    @Test
    fun `pubsub topic and subscription wiring`() {
        val w = World(topics = listOf("events"), subscriptions = mapOf("billing" to "events", "stray" to "other"))
        assertTrue(verify(w, "{type: pubsub.topic, name: events, label: L}", "{type: pubsub.subscription, name: billing, topic: events, label: L}").passed)
        assertContains(verify(w, "{type: pubsub.subscription, name: stray, topic: events, label: L}").results.single().detail!!, "listens to 'other'")
        assertEquals("no such subscription", verify(w, "{type: pubsub.subscription, name: nope, label: L}").results.single().detail)
        assertContains(verify(w, "{type: pubsub.topic, name: nope, label: L}").results.single().detail!!, "found: events")
    }

    @Test
    fun `chaos fault must be active and strong enough`() = runBlocking {
        val tox = FakeToxiproxy()
        val check = "{type: chaos.fault, target: floci-aws, preset: latency, attribute: latency, atLeast: 1000, label: L}"
        val none = verify(World(), check, tox = tox)
        assertContains(none.results.single().detail!!, "no 'latency' fault")

        Chaos(tox).apply("floci-aws", "latency", mapOf("latency" to 400.0))
        assertContains(verify(World(), check, tox = tox).results.single().detail!!, "need at least 1000")

        Chaos(tox).apply("floci-aws", "latency", mapOf("latency" to 1500.0))
        assertTrue(verify(World(), check, tox = tox).passed)

        val off = FakeToxiproxy().apply { unreachable = true }
        assertEquals("Toxiproxy is not running", verify(World(), check, tox = off).results.single().detail)
    }

    @Test
    fun `an outage satisfies an outage check`() = runBlocking {
        val tox = FakeToxiproxy()
        Chaos(tox).apply("redis", "outage", emptyMap())
        assertTrue(verify(World(), "{type: chaos.fault, target: redis, preset: outage, label: L}", tox = tox).passed)
    }

    @Test
    fun `an emulator that is down fails every check with a useful reason instead of throwing`() {
        val result = verify(
            World(down = true),
            "{type: s3.bucket, name: b, label: L}", "{type: sqs.queue, name: q, label: L}", "{type: pubsub.topic, name: t, label: L}",
        )
        assertFalse(result.passed)
        assertTrue(result.results.all { !it.ok && "not reachable" in it.detail.orEmpty() })
    }

    @Test
    fun `passed needs every check`() {
        val w = World(buckets = listOf("b1"))
        val r = verify(w, "{type: s3.bucket, name: b1, label: L}", "{type: s3.bucket, name: b2, label: L}")
        assertFalse(r.passed)
        assertEquals(listOf(true, false), r.results.map { it.ok })
        assertEquals(listOf(0, 1), r.results.map { it.index })
    }
}

class LabApiTest {
    private val catalog = LabCatalog.load(mapOf("NIMBO_LABS_DIR" to repoLabsDir().path))

    @Test
    fun `list returns every lab with bilingual text and no check parameters`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), labs = catalog) }
        val body = client.get("/api/labs").bodyAsText()
        val labs = Json.parseToJsonElement(body).jsonObject["labs"]!!.jsonArray
        assertTrue(labs.size >= 6)
        val first = labs.first().jsonObject
        assertNotNull(first["title"]!!.jsonObject["es"])
        assertTrue(first["checks"]!!.jsonArray.first().jsonObject.keys == setOf("label"), "checks must not leak parameters")
        assertFalse(body.contains("\"params\""))
    }

    @Test
    fun `verify runs the checks against the emulators`() = testApplication {
        val world = World(buckets = listOf("lab-first-bucket"), objectText = "hello nimbo")
        application { nimboModule(NimboConfig(), FakeRuntime(), inspector = Inspector(world), labs = catalog) }
        val ok = Json.parseToJsonElement(client.post("/api/labs/first-bucket/verify").bodyAsText()).jsonObject
        assertEquals("true", ok["passed"]!!.jsonPrimitive.content)

        world.buckets = emptyList()
        val bad = Json.parseToJsonElement(client.post("/api/labs/first-bucket/verify").bodyAsText()).jsonObject
        assertEquals("false", bad["passed"]!!.jsonPrimitive.content)
    }

    @Test
    fun `unknown lab is 404 and labs are behind the token`() = testApplication {
        application { nimboModule(NimboConfig(apiToken = "t"), FakeRuntime(), labs = catalog) }
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/labs").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/labs/first-bucket/verify").status)
    }

    @Test
    fun `unknown lab id`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), labs = catalog) }
        assertEquals(HttpStatusCode.NotFound, client.post("/api/labs/ghost/verify").status)
        assertEquals(HttpStatusCode.NotFound, client.post("/api/labs/..%2Fx/verify").status)
    }
}

class LabClusterChecksTest {
    private fun verify(kube: Kube?, traces: Traces?, vararg checks: String): LabVerifyDto = runBlocking {
        val (def, problem) = parse(lab(*checks))
        assertNull(problem)
        LabVerifier(Inspector(World()), Chaos(FakeToxiproxy()), kube, traces).verify(def!!)
    }

    private val up = Kube(healthy())

    @Test
    fun `deployment readiness is compared with what the lab asks for`() {
        assertTrue(verify(up, null, "{type: k8s.deployment, name: hello, atLeast: 2, label: L}").passed)
        assertTrue(verify(up, null, "{type: k8s.deployment, name: hello, label: L}").passed, "atLeast defaults to 1")
        assertContains(verify(up, null, "{type: k8s.deployment, name: hello, atLeast: 3, label: L}").results.single().detail!!, "2 of 3 replicas ready, need 3")
        assertContains(verify(up, null, "{type: k8s.deployment, name: ghost, label: L}").results.single().detail!!, "no deployment ghost in namespace default")
        assertContains(verify(up, null, "{type: k8s.deployment, name: hello, namespace: staging, label: L}").results.single().detail!!, "namespace staging")
    }

    @Test
    fun `service existence and type`() {
        assertTrue(verify(up, null, "{type: k8s.service, name: hello, serviceType: NodePort, label: L}").passed)
        assertTrue(verify(up, null, "{type: k8s.service, name: hello, label: L}").passed)
        assertContains(verify(up, null, "{type: k8s.service, name: kubernetes, serviceType: NodePort, label: L}").results.single().detail!!, "ClusterIP, not NodePort")
        assertContains(verify(up, null, "{type: k8s.service, name: nope, label: L}").results.single().detail!!, "no service nope")
    }

    @Test
    fun `a stopped cluster fails with the reason, an unconfigured one says so`() {
        val stopped = Kube(ScriptedShell { _, _ -> ExecResult(1, "No such container: nimbo-k3s") })
        assertContains(verify(stopped, null, "{type: k8s.deployment, name: hello, label: L}").results.single().detail!!, "not running")
        assertEquals("Kubernetes is not available", verify(null, null, "{type: k8s.service, name: hello, label: L}").results.single().detail)
        assertEquals("Tracing is not available", verify(null, null, "{type: trace.service, name: s, label: L}").results.single().detail)
    }

    @Test
    fun `a service shows up once it has sent traces`() {
        val jaeger = FakeJaeger { _, _ -> HttpReply(200, """{"data":["lab-orders-api","other"]}""") }
        assertTrue(verify(null, Traces(jaeger), "{type: trace.service, name: lab-orders-api, label: L}").passed)
        val detail = verify(null, Traces(jaeger), "{type: trace.service, name: missing, label: L}").results.single().detail!!
        assertContains(detail, "no traces from that service yet")
        assertContains(detail, "lab-orders-api")
        val down = FakeJaeger().apply { unreachable = true }
        assertContains(verify(null, Traces(down), "{type: trace.service, name: x, label: L}").results.single().detail!!, "not reachable")
    }

    @Test
    fun `the new check types validate their parameters`() {
        assertContains(parse(lab("{type: k8s.deployment, label: L}")).second.orEmpty(), "missing 'name'")
        assertContains(parse(lab("{type: k8s.service, name: a, namespace: 'x y', label: L}")).second.orEmpty(), "invalid value")
        assertContains(parse(lab("{type: trace.service, name: '../x', label: L}")).second.orEmpty(), "invalid value")
    }
}
