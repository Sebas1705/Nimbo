package es.sebas1705.nimbo

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import org.w3c.dom.Element
import java.io.IOException
import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import javax.xml.parsers.DocumentBuilderFactory

class EmulatorUnreachable(message: String) : Exception(message)
class EmulatorError(val status: Int, message: String) : Exception(message)
class InvalidName(message: String) : Exception(message)

data class HttpReply(val status: Int, val body: String)

fun interface HttpGateway {
    suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply
}

class JavaHttpGateway : HttpGateway {
    // HTTP/1.1 on purpose: it is what curl and most SDKs speak, so results match what your own code will see.
    private val client = HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1).connectTimeout(Duration.ofSeconds(3)).build()

    override suspend fun send(method: String, url: String, headers: Map<String, String>, body: String?): HttpReply =
        withContext(Dispatchers.IO) {
            try {
                val builder = HttpRequest.newBuilder(URI.create(url)).timeout(Duration.ofSeconds(10))
                headers.forEach { (k, v) -> builder.header(k, v) }
                builder.method(method, if (body == null) HttpRequest.BodyPublishers.noBody() else HttpRequest.BodyPublishers.ofString(body))
                val reply = client.send(builder.build(), HttpResponse.BodyHandlers.ofString())
                HttpReply(reply.statusCode(), reply.body())
            } catch (e: IOException) {
                throw EmulatorUnreachable("Emulator not reachable at ${URI.create(url).let { "${it.host}:${it.port}" }}")
            }
        }
}

// ---- DTOs ----

@Serializable data class BucketDto(val name: String, val created: String? = null)
@Serializable data class S3ObjectDto(val key: String, val size: Long, val lastModified: String? = null)
@Serializable data class ObjectListDto(val bucket: String, val objects: List<S3ObjectDto>, val truncated: Boolean)
@Serializable data class ObjectPreviewDto(val bucket: String, val key: String, val text: String, val truncated: Boolean)

@Serializable data class QueueDto(val name: String, val url: String, val messages: Int?, val inFlight: Int?, val delayed: Int?)
@Serializable data class QueueMessageDto(val id: String, val body: String, val receiveCount: Int?, val sentAt: Long?)
@Serializable data class QueueMessagesDto(val queue: String, val messages: List<QueueMessageDto>)

@Serializable data class TableDto(val name: String, val status: String?, val items: Long?, val keys: List<String>)
@Serializable data class ItemsDto(val table: String, val items: List<JsonElement>, val count: Int)

@Serializable data class TopicDto(val name: String)
@Serializable data class SubscriptionDto(val name: String, val topic: String?)
@Serializable data class PubSubDto(val project: String, val topics: List<TopicDto>, val subscriptions: List<SubscriptionDto>)

/**
 * Read-only views over what is stored in the local emulators. The emulators do not check signatures, so a
 * fixed placeholder Authorization header is enough and no credentials are involved.
 */
class Inspector(
    private val http: HttpGateway,
    private val awsEndpoint: String = "http://localhost:4566",
    private val gcpEndpoint: String = "http://localhost:4588",
) {
    private val json = Json { ignoreUnknownKeys = true }

    // ---------- S3 ----------

    suspend fun buckets(): List<BucketDto> {
        return xml(aws("s3", "GET", "/")).elements("Bucket").map { BucketDto(it.text("Name").orEmpty(), it.text("CreationDate")) }
    }

    suspend fun objects(bucket: String, prefix: String = "", max: Int = 200): ObjectListDto {
        name(bucket, "bucket")
        val query = "list-type=2&max-keys=${max.coerceIn(1, 1000)}" + if (prefix.isNotEmpty()) "&prefix=${enc(prefix)}" else ""
        val root = xml(aws("s3", "GET", "/$bucket?$query"))
        val objects = root.elements("Contents").map { S3ObjectDto(it.text("Key").orEmpty(), it.text("Size")?.toLongOrNull() ?: 0, it.text("LastModified")) }
        return ObjectListDto(bucket, objects, root.text("IsTruncated") == "true")
    }

    suspend fun preview(bucket: String, key: String, maxBytes: Int = 4096): ObjectPreviewDto {
        name(bucket, "bucket")
        val segments = key.split('/')
        if (key.isEmpty() || segments.any { it == "." || it == ".." }) throw InvalidName("Invalid object key")
        val path = "/$bucket/" + segments.joinToString("/") { enc(it) }
        val reply = aws("s3", "GET", path, extra = mapOf("Range" to "bytes=0-${maxBytes - 1}"))
        val bytes = reply.body
        return ObjectPreviewDto(bucket, key, bytes.take(maxBytes), truncated = bytes.length >= maxBytes)
    }

    // ---------- SQS ----------

    suspend fun queues(): List<QueueDto> {
        val root = xml(aws("sqs", "POST", "/", form = "Action=ListQueues&Version=2012-11-05"))
        return root.elements("QueueUrl").mapNotNull { it.textContent.trim().takeIf(String::isNotEmpty) }.map { url ->
            val (account, name) = queueParts(url)
            val attrs = runCatching { queueAttributes(account, name) }.getOrDefault(emptyMap())
            QueueDto(
                name = name,
                url = "$awsEndpoint/$account/$name",
                messages = attrs["ApproximateNumberOfMessages"]?.toIntOrNull(),
                inFlight = attrs["ApproximateNumberOfMessagesNotVisible"]?.toIntOrNull(),
                delayed = attrs["ApproximateNumberOfMessagesDelayed"]?.toIntOrNull(),
            )
        }
    }

    /** Peeks with visibility timeout 0, so messages stay available (receive count still goes up, as on real SQS). */
    suspend fun messages(queue: String, account: String = "000000000000"): QueueMessagesDto {
        name(queue, "queue")
        name(account, "account")
        val form = "Action=ReceiveMessage&MaxNumberOfMessages=10&VisibilityTimeout=0&AttributeName.1=All&Version=2012-11-05"
        val root = xml(aws("sqs", "POST", "/$account/$queue", form = form))
        val messages = root.elements("Message").map { m ->
            val attrs = m.elements("Attribute").associate { it.text("Name").orEmpty() to it.text("Value").orEmpty() }
            QueueMessageDto(
                id = m.text("MessageId").orEmpty(),
                body = m.text("Body").orEmpty(),
                receiveCount = attrs["ApproximateReceiveCount"]?.toIntOrNull(),
                sentAt = attrs["SentTimestamp"]?.toLongOrNull(),
            )
        }
        return QueueMessagesDto(queue, messages)
    }

    private suspend fun queueAttributes(account: String, queue: String): Map<String, String> {
        val root = xml(aws("sqs", "POST", "/$account/$queue", form = "Action=GetQueueAttributes&AttributeName.1=All&Version=2012-11-05"))
        return root.elements("Attribute").associate { it.text("Name").orEmpty() to it.text("Value").orEmpty() }
    }

    private fun queueParts(url: String): Pair<String, String> {
        val parts = URI.create(url).path.trim('/').split('/')
        if (parts.size != 2) throw EmulatorError(502, "Unexpected queue URL: $url")
        name(parts[0], "account")
        name(parts[1], "queue")
        return parts[0] to parts[1]
    }

    // ---------- DynamoDB ----------

    suspend fun tables(): List<TableDto> {
        val names = dynamo("ListTables", "{}").jsonObject["TableNames"]?.jsonArray?.map { it.jsonPrimitive.content }.orEmpty()
        return names.map { n ->
            val table = runCatching { dynamo("DescribeTable", """{"TableName":"$n"}""").jsonObject["Table"]?.jsonObject }.getOrNull()
            TableDto(
                name = n,
                status = table?.get("TableStatus")?.jsonPrimitive?.contentOrNull,
                items = table?.get("ItemCount")?.jsonPrimitive?.longOrNull,
                keys = table?.get("KeySchema")?.jsonArray?.map { it.jsonObject["AttributeName"]!!.jsonPrimitive.content }.orEmpty(),
            )
        }
    }

    suspend fun items(table: String, limit: Int = 25): ItemsDto {
        name(table, "table")
        val reply = dynamo("Scan", """{"TableName":"$table","Limit":${limit.coerceIn(1, 100)}}""").jsonObject
        val items = reply["Items"]?.jsonArray?.map { simplifyDynamo(it) }.orEmpty()
        return ItemsDto(table, items, items.size)
    }

    private suspend fun dynamo(operation: String, body: String): JsonElement {
        val reply = http.send(
            "POST", "$awsEndpoint/",
            authHeader("dynamodb") + mapOf("X-Amz-Target" to "DynamoDB_20120810.$operation", "Content-Type" to "application/x-amz-json-1.0"),
            body,
        )
        if (reply.status !in 200..299) throw EmulatorError(reply.status, "DynamoDB $operation failed: ${reply.body.take(200)}")
        return json.parseToJsonElement(reply.body)
    }

    // ---------- Pub/Sub (GCP) ----------

    suspend fun pubsub(project: String): PubSubDto {
        if (!PROJECT.matches(project)) throw InvalidName("Invalid project id")
        suspend fun list(kind: String): JsonObject {
            val reply = http.send("GET", "$gcpEndpoint/v1/projects/$project/$kind", emptyMap(), null)
            if (reply.status !in 200..299) throw EmulatorError(reply.status, "Pub/Sub $kind failed: ${reply.body.take(200)}")
            return json.parseToJsonElement(reply.body.ifBlank { "{}" }).jsonObject
        }
        val topics = list("topics")["topics"]?.jsonArray?.map { TopicDto(it.jsonObject["name"]!!.jsonPrimitive.content.substringAfterLast('/')) }.orEmpty()
        val subs = list("subscriptions")["subscriptions"]?.jsonArray?.map {
            SubscriptionDto(
                it.jsonObject["name"]!!.jsonPrimitive.content.substringAfterLast('/'),
                it.jsonObject["topic"]?.jsonPrimitive?.contentOrNull?.substringAfterLast('/'),
            )
        }.orEmpty()
        return PubSubDto(project, topics, subs)
    }

    // ---------- plumbing ----------

    private fun authHeader(service: String) =
        mapOf("Authorization" to "AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/$service/aws4_request, SignedHeaders=host, Signature=0")

    private suspend fun aws(service: String, method: String, path: String, form: String? = null, extra: Map<String, String> = emptyMap()): HttpReply {
        val headers = authHeader(service) + extra + if (form != null) mapOf("Content-Type" to "application/x-www-form-urlencoded") else emptyMap()
        val reply = http.send(method, awsEndpoint + path, headers, form)
        if (reply.status !in 200..299) {
            val code = Regex("<Code>([^<]{1,80})</Code>").find(reply.body)?.groupValues?.get(1)
            throw EmulatorError(reply.status, "$service ${reply.status}: ${code ?: reply.body.take(120)}")
        }
        return reply
    }

    private fun xml(reply: HttpReply): Element {
        val factory = DocumentBuilderFactory.newInstance().apply {
            setFeature("http://apache.org/xml/features/disallow-doctype-decl", true)
            isXIncludeAware = false
            isExpandEntityReferences = false
        }
        return try {
            factory.newDocumentBuilder().parse(reply.body.byteInputStream()).documentElement
        } catch (e: Exception) {
            throw EmulatorError(502, "Unreadable XML from emulator")
        }
    }

    private fun name(value: String, what: String) {
        if (!NAME.matches(value)) throw InvalidName("Invalid $what name")
    }

    private fun enc(value: String) = URLEncoder.encode(value, Charsets.UTF_8).replace("+", "%20")

    companion object {
        private val NAME = Regex("[A-Za-z0-9._-]{1,255}")
        private val PROJECT = Regex("[a-z0-9][a-z0-9-]{0,62}")

        /** DynamoDB's typed attribute values ({"S":"x"}) to plain JSON. */
        fun simplifyDynamo(value: JsonElement): JsonElement {
            val obj = value as? JsonObject ?: return value
            if (obj.size != 1) {
                return buildJsonObject { obj.forEach { (k, v) -> put(k, simplifyDynamo(v)) } }
            }
            val (type, inner) = obj.entries.first()
            return when (type) {
                "S", "B" -> inner
                "N" -> inner.jsonPrimitive.let { p -> p.longOrNull?.let(::JsonPrimitive) ?: p.doubleOrNull?.let(::JsonPrimitive) ?: p }
                "BOOL" -> JsonPrimitive(inner.jsonPrimitive.booleanOrNull ?: false)
                "NULL" -> JsonNull
                "L" -> buildJsonArray { inner.jsonArray.forEach { add(simplifyDynamo(it)) } }
                "SS", "BS" -> inner
                "NS" -> buildJsonArray { inner.jsonArray.forEach { add(simplifyDynamo(buildJsonObject { put("N", it) })) } }
                "M" -> buildJsonObject { inner.jsonObject.forEach { (k, v) -> put(k, simplifyDynamo(v)) } }
                else -> buildJsonObject { obj.forEach { (k, v) -> put(k, simplifyDynamo(v)) } }
            }
        }
    }
}

// ---- tiny DOM helpers ----

private fun Element.elements(tag: String): List<Element> {
    val nodes = getElementsByTagName(tag)
    return (0 until nodes.length).mapNotNull { nodes.item(it) as? Element }
}

private fun Element.text(tag: String): String? = elements(tag).firstOrNull()?.textContent
