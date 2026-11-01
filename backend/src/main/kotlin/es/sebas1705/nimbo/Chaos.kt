package es.sebas1705.nimbo

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlin.math.roundToLong

class InvalidParam(message: String) : Exception(message)
class UnknownTarget(message: String) : Exception(message)

/** An emulator endpoint that can be reached through Toxiproxy on [proxyPort] instead of [upstreamPort]. */
data class ChaosTarget(val id: String, val name: String, val upstreamPort: Int, val proxyPort: Int)

object ChaosTargets {
    const val API_PORT = 8474

    val all = listOf(
        ChaosTarget("floci-aws", "Floci AWS", 4566, 14566),
        ChaosTarget("floci-azure", "Floci Azure", 4577, 14577),
        ChaosTarget("floci-gcp", "Floci GCP", 4588, 14588),
        ChaosTarget("floci-oci", "Floci OCI", 4599, 14599),
        ChaosTarget("azurite-blob", "Azurite Blob", 10000, 20000),
        ChaosTarget("azurite-queue", "Azurite Queue", 10001, 20001),
        ChaosTarget("azurite-table", "Azurite Table", 10002, 20002),
        ChaosTarget("minio", "MinIO", 9000, 19000),
        ChaosTarget("postgres", "PostgreSQL", 5432, 15432),
        ChaosTarget("redis", "Redis", 6379, 16379),
    )

    fun find(id: String) = all.firstOrNull { it.id == id }
}

@Serializable data class ParamDto(val key: String, val label: String, val unit: String, val min: Double, val max: Double, val default: Double)
@Serializable data class PresetDto(val id: String, val label: String, val description: String, val params: List<ParamDto>)
@Serializable data class ToxicDto(val name: String, val type: String, val toxicity: Double, val attributes: JsonObject)
@Serializable
data class ChaosTargetDto(
    val id: String,
    val name: String,
    val upstreamPort: Int,
    val proxyPort: Int,
    val endpoint: String,
    /** The proxy exists in Toxiproxy (it is created on first use). */
    val active: Boolean,
    val enabled: Boolean,
    val toxics: List<ToxicDto>,
)
@Serializable data class ChaosDto(val available: Boolean, val presets: List<PresetDto>, val targets: List<ChaosTargetDto>)
@Serializable data class ApplyDto(val params: Map<String, Double> = emptyMap())

private class Preset(val id: String, val label: String, val description: String, val toxic: String?, val params: List<ParamDto>) {
    fun dto() = PresetDto(id, label, description, params + if (toxic != null) listOf(TOXICITY) else emptyList())
    companion object {
        val TOXICITY = ParamDto("toxicity", "Affected connections", "%", 1.0, 100.0, 100.0)
    }
}

/**
 * Drives a Toxiproxy server. A proxy per target is created lazily, and presets map onto Toxiproxy toxics
 * (always on the downstream, i.e. the emulator's responses).
 */
class Chaos(private val http: HttpGateway, private val base: String = "http://localhost:${ChaosTargets.API_PORT}") {
    private val json = Json { ignoreUnknownKeys = true }

    private val presets = listOf(
        Preset("latency", "Latency", "Delay every response", "latency", listOf(
            ParamDto("latency", "Latency", "ms", 0.0, 30_000.0, 500.0),
            ParamDto("jitter", "Jitter", "ms", 0.0, 10_000.0, 100.0),
        )),
        Preset("bandwidth", "Slow link", "Limit throughput", "bandwidth", listOf(ParamDto("rate", "Rate", "KB/s", 1.0, 100_000.0, 10.0))),
        Preset("timeout", "Hang", "Stop all data, then close after the timeout (0 = never)", "timeout", listOf(ParamDto("timeout", "Timeout", "ms", 0.0, 60_000.0, 5_000.0))),
        Preset("reset", "Connection reset", "Reset the TCP connection (RST)", "reset_peer", listOf(ParamDto("timeout", "After", "ms", 0.0, 60_000.0, 0.0))),
        Preset("slow_close", "Slow close", "Delay closing the connection", "slow_close", listOf(ParamDto("delay", "Delay", "ms", 0.0, 60_000.0, 2_000.0))),
        Preset("limit_data", "Cut after N bytes", "Close the connection after N bytes", "limit_data", listOf(ParamDto("bytes", "Bytes", "B", 1.0, 100_000_000.0, 1_000.0))),
        Preset("outage", "Outage", "Refuse every connection (disable the proxy)", null, emptyList()),
    ).associateBy { it.id }

    suspend fun view(): ChaosDto {
        val live = try {
            proxies()
        } catch (e: EmulatorUnreachable) {
            return ChaosDto(false, presetDtos(), ChaosTargets.all.map { render(it, null) })
        }
        return ChaosDto(true, presetDtos(), ChaosTargets.all.map { render(it, live[it.id]) })
    }

    suspend fun apply(targetId: String, presetId: String, params: Map<String, Double>): ChaosTargetDto {
        val target = ChaosTargets.find(targetId) ?: throw UnknownTarget("Unknown target '$targetId'")
        val preset = presets[presetId] ?: throw InvalidParam("Unknown preset '$presetId'")
        val attributes = preset.dto().params.associate { p ->
            val value = params[p.key] ?: p.default
            if (value.isNaN() || value < p.min || value > p.max) throw InvalidParam("${p.key} must be between ${p.min.toLong()} and ${p.max.toLong()}")
            p.key to value
        }

        ensureProxy(target)
        if (preset.toxic == null) {
            ok(send("POST", "/proxies/${target.id}", """{"enabled":false}"""))
        } else {
            ok(send("POST", "/proxies/${target.id}", """{"enabled":true}"""))
            // Replacing is simpler than updating in place: delete (a 404 is fine) and create again.
            send("DELETE", "/proxies/${target.id}/toxics/${preset.id}", null)
            val toxic = buildJsonObject {
                put("name", preset.id)
                put("type", preset.toxic)
                put("stream", "downstream")
                put("toxicity", (attributes.getValue("toxicity") / 100.0))
                put("attributes", buildJsonObject {
                    attributes.filterKeys { it != "toxicity" }.forEach { (k, v) -> put(k, JsonPrimitive(v.roundToLong())) }
                })
            }
            ok(send("POST", "/proxies/${target.id}/toxics", toxic.toString()))
        }
        return current(target)
    }

    /** Removes every toxic of the target and turns the proxy back on. */
    suspend fun clear(targetId: String): ChaosTargetDto {
        val target = ChaosTargets.find(targetId) ?: throw UnknownTarget("Unknown target '$targetId'")
        val proxy = proxies()[target.id]
        if (proxy != null) {
            toxicsOf(proxy).forEach { send("DELETE", "/proxies/${target.id}/toxics/${it.name}", null) }
            ok(send("POST", "/proxies/${target.id}", """{"enabled":true}"""))
        }
        return current(target)
    }

    suspend fun removeToxic(targetId: String, name: String): ChaosTargetDto {
        val target = ChaosTargets.find(targetId) ?: throw UnknownTarget("Unknown target '$targetId'")
        if (!TOXIC_NAME.matches(name)) throw InvalidParam("Invalid toxic name")
        ok(send("DELETE", "/proxies/${target.id}/toxics/$name", null))
        return current(target)
    }

    suspend fun resetAll(): ChaosDto {
        ok(send("POST", "/reset", null))
        return view()
    }

    // ---- plumbing ----

    private fun presetDtos() = presets.values.map { it.dto() }

    private suspend fun current(target: ChaosTarget) = render(target, proxies()[target.id])

    private suspend fun ensureProxy(target: ChaosTarget) {
        if (proxies().containsKey(target.id)) return
        val body = buildJsonObject {
            put("name", target.id)
            put("listen", "0.0.0.0:${target.proxyPort}")
            put("upstream", "host.docker.internal:${target.upstreamPort}")
            put("enabled", true)
        }
        ok(send("POST", "/proxies", body.toString()))
    }

    private suspend fun proxies(): Map<String, JsonObject> {
        val reply = ok(send("GET", "/proxies", null))
        return json.parseToJsonElement(reply.body.ifBlank { "{}" }).jsonObject.mapValues { it.value.jsonObject }
    }

    private fun toxicsOf(proxy: JsonObject): List<ToxicDto> = proxy["toxics"]?.jsonArray?.map {
        val t = it.jsonObject
        ToxicDto(
            name = t["name"]!!.jsonPrimitive.content,
            type = t["type"]!!.jsonPrimitive.content,
            toxicity = t["toxicity"]?.jsonPrimitive?.contentOrNull?.toDoubleOrNull() ?: 1.0,
            attributes = t["attributes"] as? JsonObject ?: JsonObject(emptyMap()),
        )
    }.orEmpty()

    private fun render(target: ChaosTarget, proxy: JsonObject?) = ChaosTargetDto(
        id = target.id,
        name = target.name,
        upstreamPort = target.upstreamPort,
        proxyPort = target.proxyPort,
        endpoint = "http://localhost:${target.proxyPort}",
        active = proxy != null,
        enabled = proxy?.get("enabled")?.jsonPrimitive?.contentOrNull != "false",
        toxics = proxy?.let(::toxicsOf).orEmpty(),
    )

    private suspend fun send(method: String, path: String, body: String?): HttpReply =
        http.send(method, base + path, if (body != null) mapOf("Content-Type" to "application/json") else emptyMap(), body)

    private fun ok(reply: HttpReply): HttpReply {
        if (reply.status !in 200..299) throw EmulatorError(reply.status, "Toxiproxy ${reply.status}: ${reply.body.take(120)}")
        return reply
    }

    private companion object {
        val TOXIC_NAME = Regex("[a-z_]{1,32}")
    }
}
