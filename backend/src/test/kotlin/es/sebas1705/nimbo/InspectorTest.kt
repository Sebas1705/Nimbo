package es.sebas1705.nimbo

import io.ktor.client.request.get
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpStatusCode
import io.ktor.server.testing.testApplication
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonArray
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private data class Sent(val method: String, val url: String, val headers: Map<String, String>, val body: String?)

/** Replies by matching on method + URL + body; records every call. */
private class FakeGateway(private val rules: List<Pair<(Sent) -> Boolean, HttpReply>> = emptyList()) : HttpGateway {
    val sent = mutableListOf<Sent>()
    var unreachable = false

    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply {
        val call = Sent(method, url, headers, body)
        sent += call
        if (unreachable) throw EmulatorUnreachable("down")
        return rules.firstOrNull { it.first(call) }?.second ?: HttpReply(404, "no rule")
    }
}

private fun on(predicate: (Sent) -> Boolean, status: Int = 200, body: String) = predicate to HttpReply(status, body)

private const val S3_BUCKETS = """<?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>f7b1</ID><DisplayName>floci</DisplayName></Owner><Buckets><Bucket><Name>probe-bkt</Name><CreationDate>2026-11-01T18:12:45Z</CreationDate></Bucket><Bucket><Name>second</Name><CreationDate>2026-11-02T10:00:00Z</CreationDate></Bucket></Buckets></ListAllMyBucketsResult>"""

private const val S3_OBJECTS = """<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>probe-bkt</Name><Prefix></Prefix><MaxKeys>1000</MaxKeys><KeyCount>1</KeyCount><IsTruncated>true</IsTruncated><Contents><Key>dir/hello world.txt</Key><LastModified>2026-11-01T18:12:46Z</LastModified><ETag>&quot;fd33&quot;</ETag><Size>8</Size><StorageClass>STANDARD</StorageClass></Contents></ListBucketResult>"""

private const val SQS_LIST = """<ListQueuesResponse><ListQueuesResult><QueueUrl>http://localhost:4566/000000000000/probe-q</QueueUrl></ListQueuesResult></ListQueuesResponse>"""

private const val SQS_ATTRS = """<GetQueueAttributesResponse><GetQueueAttributesResult><Attribute><Name>VisibilityTimeout</Name><Value>30</Value></Attribute><Attribute><Name>ApproximateNumberOfMessages</Name><Value>3</Value></Attribute><Attribute><Name>ApproximateNumberOfMessagesNotVisible</Name><Value>1</Value></Attribute><Attribute><Name>ApproximateNumberOfMessagesDelayed</Name><Value>2</Value></Attribute></GetQueueAttributesResult></GetQueueAttributesResponse>"""

private const val SQS_MESSAGES = """<ReceiveMessageResponse><ReceiveMessageResult><Message><MessageId>m-1</MessageId><ReceiptHandle>abc</ReceiptHandle><Body>hello &amp; goodbye</Body><Attribute><Name>SentTimestamp</Name><Value>1793556777922</Value></Attribute><Attribute><Name>ApproximateReceiveCount</Name><Value>2</Value></Attribute></Message><Message><MessageId>m-2</MessageId><Body>second</Body></Message></ReceiveMessageResult></ReceiveMessageResponse>"""

private fun aws(vararg rules: Pair<(Sent) -> Boolean, HttpReply>) = FakeGateway(rules.toList())

class InspectorS3Test {
    @Test
    fun `lists buckets with creation dates`() = runBlocking {
        val gw = aws(on({ it.method == "GET" && it.url == "http://localhost:4566/" }, body = S3_BUCKETS))
        val buckets = Inspector(gw).buckets()
        assertEquals(listOf("probe-bkt", "second"), buckets.map { it.name })
        assertEquals("2026-11-01T18:12:45Z", buckets.first().created)
        assertContains(gw.sent.single().headers.getValue("Authorization"), "/s3/aws4_request")
    }

    @Test
    fun `lists objects, encodes the prefix and reports truncation`() = runBlocking {
        val gw = aws(on({ "/probe-bkt?" in it.url }, body = S3_OBJECTS))
        val result = Inspector(gw).objects("probe-bkt", prefix = "dir/a b&c")
        assertEquals("dir/hello world.txt", result.objects.single().key)
        assertEquals(8, result.objects.single().size)
        assertTrue(result.truncated)
        val url = gw.sent.single().url
        assertContains(url, "prefix=dir%2Fa%20b%26c")
        assertContains(url, "list-type=2")
    }

    @Test
    fun `preview asks for a byte range and encodes each key segment`() = runBlocking {
        val gw = aws(on({ true }, body = "hello"))
        val preview = Inspector(gw).preview("probe-bkt", "dir/hello world.txt", maxBytes = 100)
        assertEquals("hello", preview.text)
        assertFalse(preview.truncated)
        val call = gw.sent.single()
        assertEquals("http://localhost:4566/probe-bkt/dir/hello%20world.txt", call.url)
        assertEquals("bytes=0-99", call.headers["Range"])
    }

    @Test
    fun `preview flags truncated text when the limit is reached`() = runBlocking {
        val gw = aws(on({ true }, body = "x".repeat(10)))
        assertTrue(Inspector(gw).preview("b", "k", maxBytes = 10).truncated)
    }

    @Test
    fun `hostile names never reach the emulator`() = runBlocking {
        val gw = aws(on({ true }, body = "<x/>"))
        val inspector = Inspector(gw)
        listOf("../etc", "a/b", "a b", "", "x".repeat(300), "a?b=c", "a%2fb").forEach { bad ->
            assertFailsWith<InvalidName>(bad) { inspector.objects(bad) }
        }
        listOf("../secret", "a/../b", "./x", "").forEach { badKey ->
            assertFailsWith<InvalidName>(badKey) { inspector.preview("b", badKey) }
        }
        assertFailsWith<InvalidName> { inspector.messages("a/b") }
        assertFailsWith<InvalidName> { inspector.items("a b") }
        assertFailsWith<InvalidName> { inspector.pubsub("Bad Project") }
        assertTrue(gw.sent.isEmpty())
    }

    @Test
    fun `xml with a doctype is rejected instead of expanded`() {
        val evil = """<?xml version="1.0"?><!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><ListAllMyBucketsResult>&e;</ListAllMyBucketsResult>"""
        val ex = assertFailsWith<EmulatorError> { runBlocking { Inspector(aws(on({ true }, body = evil))).buckets() } }
        assertEquals(502, ex.status)
    }

    @Test
    fun `emulator errors and unreachable emulator are distinct`() {
        assertFailsWith<EmulatorError> { runBlocking { Inspector(aws(on({ true }, status = 500, body = "boom"))).buckets() } }
        val down = FakeGateway().apply { unreachable = true }
        assertFailsWith<EmulatorUnreachable> { runBlocking { Inspector(down).buckets() } }
    }
}

class InspectorSqsTest {
    private val gw = aws(
        on({ it.body?.contains("Action=ListQueues") == true }, body = SQS_LIST),
        on({ it.body?.contains("Action=GetQueueAttributes") == true }, body = SQS_ATTRS),
        on({ it.body?.contains("Action=ReceiveMessage") == true }, body = SQS_MESSAGES),
    )

    @Test
    fun `queues carry message counters`() = runBlocking {
        val q = Inspector(gw).queues().single()
        assertEquals("probe-q", q.name)
        assertEquals(3, q.messages)
        assertEquals(1, q.inFlight)
        assertEquals(2, q.delayed)
        assertEquals("http://localhost:4566/000000000000/probe-q", q.url)
    }

    @Test
    fun `queue urls from the emulator never redirect our requests to another host`() = runBlocking {
        val evil = aws(
            on({ it.body?.contains("Action=ListQueues") == true }, body = "<R><QueueUrl>http://evil.example:9999/000000000000/q1</QueueUrl></R>"),
            on({ true }, body = SQS_ATTRS),
        )
        val q = Inspector(evil).queues().single()
        assertEquals("http://localhost:4566/000000000000/q1", q.url)
        assertTrue(evil.sent.all { it.url.startsWith("http://localhost:4566/") }, evil.sent.map { it.url }.toString())
    }

    @Test
    fun `a malformed queue url is a bad gateway, not a crash`() {
        val bad = aws(on({ true }, body = "<R><QueueUrl>http://localhost:4566/a/b/c</QueueUrl></R>"))
        assertFailsWith<EmulatorError> { runBlocking { Inspector(bad).queues() } }
    }

    @Test
    fun `a queue whose attributes fail still shows up`() = runBlocking {
        val partial = aws(
            on({ it.body?.contains("Action=ListQueues") == true }, body = SQS_LIST),
            on({ true }, status = 500, body = "nope"),
        )
        val q = Inspector(partial).queues().single()
        assertEquals("probe-q", q.name)
        assertEquals(null, q.messages)
    }

    @Test
    fun `peek uses visibility timeout 0 and decodes bodies`() = runBlocking {
        val result = Inspector(gw).messages("probe-q")
        assertEquals(listOf("hello & goodbye", "second"), result.messages.map { it.body })
        assertEquals(2, result.messages.first().receiveCount)
        assertEquals(1793556777922, result.messages.first().sentAt)
        assertEquals(null, result.messages.last().receiveCount)
        val call = gw.sent.last()
        assertContains(call.body!!, "VisibilityTimeout=0")
        assertEquals("POST", call.method)
        assertContains(call.url, "/000000000000/probe-q")
    }
}

class InspectorDynamoTest {
    private val describe = """{"Table":{"TableName":"probe-t","TableStatus":"ACTIVE","ItemCount":7,"KeySchema":[{"AttributeName":"id","KeyType":"HASH"},{"AttributeName":"ts","KeyType":"RANGE"}]}}"""

    @Test
    fun `tables are described with keys and item counts`() = runBlocking {
        val gw = aws(
            on({ it.headers["X-Amz-Target"]?.endsWith("ListTables") == true }, body = """{"TableNames":["probe-t"]}"""),
            on({ it.headers["X-Amz-Target"]?.endsWith("DescribeTable") == true }, body = describe),
        )
        val t = Inspector(gw).tables().single()
        assertEquals("probe-t", t.name)
        assertEquals("ACTIVE", t.status)
        assertEquals(7, t.items)
        assertEquals(listOf("id", "ts"), t.keys)
        assertTrue(gw.sent.all { it.headers["Content-Type"] == "application/x-amz-json-1.0" })
    }

    @Test
    fun `scan result is flattened from dynamodb typed values`() = runBlocking {
        val body = """{"Items":[{"id":{"S":"1"},"n":{"N":"42"},"f":{"N":"1.5"},"tags":{"L":[{"S":"a"},{"N":"2"}]},"ok":{"BOOL":true},"gone":{"NULL":true},"nested":{"M":{"x":{"S":"y"}}},"set":{"SS":["p","q"]}}],"Count":1}"""
        val gw = aws(on({ true }, body = body))
        val items = Inspector(gw).items("probe-t")
        val item = items.items.single().jsonObject
        assertEquals(JsonPrimitive("1"), item["id"])
        assertEquals(JsonPrimitive(42L), item["n"])
        assertEquals(JsonPrimitive(1.5), item["f"])
        assertEquals(JsonPrimitive(true), item["ok"])
        assertEquals(JsonNull, item["gone"])
        assertEquals(2, item["tags"]!!.jsonArray.size)
        assertEquals(JsonPrimitive("y"), item["nested"]!!.jsonObject["x"])
        assertEquals(2, item["set"]!!.jsonArray.size)
        assertContains(gw.sent.single().body!!, "\"Limit\":25")
    }

    @Test
    fun `limit is capped`() = runBlocking {
        val gw = aws(on({ true }, body = """{"Items":[]}"""))
        Inspector(gw).items("t", limit = 100_000)
        assertContains(gw.sent.single().body!!, "\"Limit\":100")
    }

    @Test
    fun `a failing dynamodb call is a bad gateway`() {
        assertFailsWith<EmulatorError> { runBlocking { Inspector(aws(on({ true }, status = 400, body = "{}"))).items("t") } }
    }
}

class InspectorPubSubTest {
    @Test
    fun `topics and subscriptions are listed with short names`() = runBlocking {
        val gw = FakeGateway(
            listOf(
                on({ it.url.endsWith("/topics") }, body = """{"topics":[{"name":"projects/p1/topics/orders"}]}"""),
                on({ it.url.endsWith("/subscriptions") }, body = """{"subscriptions":[{"name":"projects/p1/subscriptions/billing","topic":"projects/p1/topics/orders"}]}"""),
            ),
        )
        val result = Inspector(gw).pubsub("p1")
        assertEquals(listOf("orders"), result.topics.map { it.name })
        assertEquals("billing", result.subscriptions.single().name)
        assertEquals("orders", result.subscriptions.single().topic)
        assertEquals("http://localhost:4588/v1/projects/p1/topics", gw.sent.first().url)
    }

    @Test
    fun `an empty project returns empty lists`() = runBlocking {
        val gw = FakeGateway(listOf(on({ true }, body = "{}")))
        val result = Inspector(gw).pubsub("empty-project")
        assertTrue(result.topics.isEmpty() && result.subscriptions.isEmpty())
    }
}

class InspectApiTest {
    private val gateway = aws(
        on({ it.method == "GET" && it.url == "http://localhost:4566/" }, body = S3_BUCKETS),
    )

    @Test
    fun `buckets endpoint serializes`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), inspector = Inspector(gateway)) }
        val body = client.get("/api/inspect/aws/s3/buckets").bodyAsText()
        val names = Json.parseToJsonElement(body).jsonArray.map { it.jsonObject["name"]!!.toString().trim('"') }
        assertEquals(listOf("probe-bkt", "second"), names)
    }

    @Test
    fun `status codes map the failure kinds`() = testApplication {
        val down = FakeGateway().apply { unreachable = true }
        application { nimboModule(NimboConfig(), FakeRuntime(), inspector = Inspector(down)) }
        assertEquals(HttpStatusCode.ServiceUnavailable, client.get("/api/inspect/aws/s3/buckets").status)
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/inspect/aws/s3/buckets/a%20b/objects").status)
        assertEquals(HttpStatusCode.BadRequest, client.get("/api/inspect/gcp/pubsub?project=NO").status)
    }

    @Test
    fun `a missing bucket is a short 404 without the raw xml`() = testApplication {
        val xml = """<?xml version="1.0"?><Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist.</Message></Error>"""
        application { nimboModule(NimboConfig(), FakeRuntime(), inspector = Inspector(aws(on({ true }, status = 404, body = xml)))) }
        val response = client.get("/api/inspect/aws/s3/buckets/nope/objects")
        assertEquals(HttpStatusCode.NotFound, response.status)
        val text = response.bodyAsText()
        assertContains(text, "NoSuchBucket")
        assertFalse(text.contains("<?xml"))
    }

    @Test
    fun `emulator failure is 502`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime(), inspector = Inspector(aws(on({ true }, status = 500, body = "x")))) }
        assertEquals(HttpStatusCode.BadGateway, client.get("/api/inspect/aws/sqs/queues").status)
    }

    @Test
    fun `inspector is behind the api token`() = testApplication {
        application { nimboModule(NimboConfig(apiToken = "t"), FakeRuntime(), inspector = Inspector(gateway)) }
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/inspect/aws/s3/buckets").status)
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/inspect/gcp/pubsub").status)
    }
}
