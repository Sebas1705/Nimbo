package es.sebas1705.nimbo

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.net.URLEncoder
import java.security.SecureRandom

@Serializable
data class TraceSummaryDto(
    val traceId: String,
    val service: String,
    val operation: String,
    /** Epoch milliseconds of the first span. */
    val startedAt: Long,
    val durationMicros: Long,
    val spans: Int,
    val errors: Int,
)

@Serializable
data class SpanDto(
    val id: String,
    val parentId: String?,
    val service: String,
    val operation: String,
    val depth: Int,
    val offsetMicros: Long,
    val durationMicros: Long,
    val error: Boolean,
)

@Serializable data class TraceDetailDto(val traceId: String, val startedAt: Long, val durationMicros: Long, val spans: List<SpanDto>)
@Serializable data class SampleTraceDto(val traceId: String, val service: String)

/** Reads traces from Jaeger's query API and can push a synthetic one through its OTLP/HTTP intake. */
class Traces(
    private val http: HttpGateway,
    private val query: String = "http://localhost:16686",
    private val otlp: String = "http://localhost:4318",
    private val random: SecureRandom = SecureRandom(),
) {
    private val json = Json { ignoreUnknownKeys = true }

    suspend fun services(): List<String> {
        val reply = get("/api/services")
        return json.parseToJsonElement(reply).jsonObject["data"]?.let { it as? JsonArray }?.map { it.jsonPrimitive.content }.orEmpty().sorted()
    }

    suspend fun list(service: String, limit: Int = 20): List<TraceSummaryDto> {
        if (!SERVICE.matches(service)) throw InvalidName("Invalid service name")
        val reply = get("/api/traces?service=${URLEncoder.encode(service, Charsets.UTF_8)}&limit=${limit.coerceIn(1, 100)}&lookback=24h")
        return (json.parseToJsonElement(reply).jsonObject["data"] as? JsonArray).orEmpty().map { summary(it.jsonObject) }
            .sortedByDescending { it.startedAt }
    }

    suspend fun detail(traceId: String): TraceDetailDto {
        if (!TRACE_ID.matches(traceId)) throw InvalidName("Invalid trace id")
        val reply = get("/api/traces/$traceId")
        val trace = (json.parseToJsonElement(reply).jsonObject["data"] as? JsonArray)?.firstOrNull()?.jsonObject
            ?: throw EmulatorError(404, "Trace not found")
        return detail(trace)
    }

    /** Sends a 3-span trace (an API call that writes to a queue and a table); optionally with a failing span. */
    suspend fun sample(failing: Boolean = false): SampleTraceDto {
        val traceId = hex(16)
        val root = hex(8)
        val now = System.currentTimeMillis() * 1_000_000L
        fun span(id: String, parent: String?, name: String, kind: Int, startMs: Long, endMs: Long, error: Boolean = false) = buildString {
            append("""{"traceId":"$traceId","spanId":"$id",""")
            if (parent != null) append(""""parentSpanId":"$parent",""")
            append(""""name":"$name","kind":$kind,"startTimeUnixNano":"${now + startMs * 1_000_000}","endTimeUnixNano":"${now + endMs * 1_000_000}",""")
            append(if (error) """"status":{"code":2,"message":"simulated failure"}}""" else """"status":{"code":1}}""")
        }
        val body = """{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"$SAMPLE_SERVICE"}}]},"scopeSpans":[{"scope":{"name":"nimbo"},"spans":[""" +
            listOf(
                span(root, null, "GET /orders", 2, 0, 120),
                span(hex(8), root, "sqs.SendMessage", 3, 10, 55),
                span(hex(8), root, "dynamodb.PutItem", 3, 60, 110, error = failing),
            ).joinToString(",") + "]}]}]}"
        val reply = http.send("POST", "$otlp/v1/traces", mapOf("Content-Type" to "application/json"), body)
        if (reply.status !in 200..299) throw EmulatorError(reply.status, "OTLP intake answered ${reply.status}: ${reply.body.take(120)}")
        return SampleTraceDto(traceId, SAMPLE_SERVICE)
    }

    // ---- parsing ----

    private class Raw(val id: String, val parent: String?, val service: String, val operation: String, val start: Long, val duration: Long, val error: Boolean)

    private fun raw(trace: JsonObject): List<Raw> {
        val processes = trace["processes"]?.jsonObject.orEmpty()
        return (trace["spans"] as? JsonArray).orEmpty().map { s ->
            val o = s.jsonObject
            val tags = (o["tags"] as? JsonArray).orEmpty().map { it.jsonObject }
            Raw(
                id = o["spanID"]!!.jsonPrimitive.content,
                parent = (o["references"] as? JsonArray).orEmpty().map { it.jsonObject }.firstOrNull { it["refType"]?.jsonPrimitive?.content == "CHILD_OF" }?.get("spanID")?.jsonPrimitive?.content,
                service = processes[o["processID"]?.jsonPrimitive?.content]?.jsonObject?.get("serviceName")?.jsonPrimitive?.contentOrNull ?: "unknown",
                operation = o["operationName"]?.jsonPrimitive?.content.orEmpty(),
                start = o["startTime"]?.jsonPrimitive?.longOrNull ?: 0,
                duration = o["duration"]?.jsonPrimitive?.longOrNull ?: 0,
                error = tags.any { it["key"]?.jsonPrimitive?.content == "error" && it["value"]?.jsonPrimitive?.content == "true" },
            )
        }
    }

    private fun summary(trace: JsonObject): TraceSummaryDto {
        val spans = raw(trace)
        val ids = spans.map { it.id }.toSet()
        val root = spans.filter { it.parent == null || it.parent !in ids }.minByOrNull { it.start } ?: spans.minByOrNull { it.start }
        val start = spans.minOfOrNull { it.start } ?: 0
        val end = spans.maxOfOrNull { it.start + it.duration } ?: 0
        return TraceSummaryDto(
            traceId = trace["traceID"]!!.jsonPrimitive.content,
            service = root?.service ?: "unknown",
            operation = root?.operation.orEmpty(),
            startedAt = start / 1000,
            durationMicros = end - start,
            spans = spans.size,
            errors = spans.count { it.error },
        )
    }

    private fun detail(trace: JsonObject): TraceDetailDto {
        val spans = raw(trace)
        val start = spans.minOfOrNull { it.start } ?: 0
        val end = spans.maxOfOrNull { it.start + it.duration } ?: 0
        val ids = spans.map { it.id }.toSet()
        val children = spans.groupBy { if (it.parent in ids) it.parent else null }
        val ordered = mutableListOf<SpanDto>()
        val seen = mutableSetOf<String>()
        fun visit(span: Raw, depth: Int) {
            if (!seen.add(span.id)) return // a malformed trace with a cycle must not loop forever
            ordered += SpanDto(span.id, span.parent.takeIf { it in ids }, span.service, span.operation, depth, span.start - start, span.duration, span.error)
            children[span.id].orEmpty().sortedBy { it.start }.forEach { visit(it, depth + 1) }
        }
        children[null].orEmpty().sortedBy { it.start }.forEach { visit(it, 0) }
        spans.filter { it.id !in seen }.sortedBy { it.start }.forEach { visit(it, 0) }
        return TraceDetailDto(trace["traceID"]!!.jsonPrimitive.content, start / 1000, end - start, ordered)
    }

    private suspend fun get(path: String): String {
        val reply = http.send("GET", query + path, emptyMap(), null)
        if (reply.status !in 200..299) throw EmulatorError(reply.status, "Jaeger answered ${reply.status}")
        return reply.body
    }

    private fun hex(bytes: Int) = ByteArray(bytes).also(random::nextBytes).joinToString("") { "%02x".format(it) }

    private fun JsonObject?.orEmpty() = this ?: JsonObject(emptyMap())
    private fun JsonArray?.orEmpty() = this ?: JsonArray(emptyList())

    companion object {
        const val SAMPLE_SERVICE = "nimbo-demo"
        private val SERVICE = Regex("[A-Za-z0-9._-]{1,128}")
        private val TRACE_ID = Regex("[0-9a-f]{16,32}")
    }
}
