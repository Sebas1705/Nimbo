package es.sebas1705.nimbo

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.yaml.snakeyaml.LoaderOptions
import org.yaml.snakeyaml.Yaml
import org.yaml.snakeyaml.constructor.SafeConstructor
import java.io.File

@Serializable data class L10nDto(val en: String, val es: String)
@Serializable data class SnippetDto(val title: L10nDto? = null, val code: String)
@Serializable data class LabCheckDto(val label: L10nDto)
@Serializable
data class LabDto(
    val id: String,
    val level: Int,
    val stack: String?,
    val title: L10nDto,
    val summary: L10nDto,
    val steps: List<L10nDto>,
    val hints: List<L10nDto>,
    val solution: List<SnippetDto>,
    val checks: List<LabCheckDto>,
)
@Serializable data class LabListDto(val labs: List<LabDto>, val problems: List<String>)
@Serializable data class LabCheckResultDto(val index: Int, val ok: Boolean, val detail: String? = null)
@Serializable data class LabVerifyDto(val id: String, val passed: Boolean, val results: List<LabCheckResultDto>)

class CheckSpec(val type: String, val params: Map<String, Any?>) {
    fun str(key: String): String? = params[key]?.toString()
    fun int(key: String, default: Int): Int = (params[key] as? Number)?.toInt() ?: default
}

class LabDef(val dto: LabDto, val checks: List<CheckSpec>)

class LabCatalog(val labs: List<LabDef>, val problems: List<String> = emptyList()) {
    fun find(id: String) = labs.firstOrNull { it.dto.id == id }

    companion object {
        private val ID = Regex("[a-z0-9][a-z0-9-]{0,40}")
        private val NAME = Regex("[A-Za-z0-9._-]{1,255}")

        /** check type -> required string params (an `atLeast`-style number is always optional). */
        private val REQUIRED = mapOf(
            "s3.bucket" to listOf("name"),
            "s3.object" to listOf("bucket", "key"),
            "sqs.queue" to listOf("name"),
            "sqs.messages" to listOf("queue"),
            "dynamodb.table" to listOf("name"),
            "dynamodb.items" to listOf("table"),
            "pubsub.topic" to listOf("name"),
            "pubsub.subscription" to listOf("name"),
            "chaos.fault" to listOf("target", "preset"),
            "k8s.deployment" to listOf("name"),
            "k8s.service" to listOf("name"),
            "trace.service" to listOf("name"),
        )
        private val NAMED = setOf("name", "bucket", "queue", "table", "topic", "project", "target", "preset", "attribute", "namespace", "serviceType")

        fun parseOne(text: String, source: String): Pair<LabDef?, String?> {
            val root = try {
                Yaml(SafeConstructor(LoaderOptions())).load<Any?>(text) as? Map<*, *>
            } catch (e: Exception) {
                return null to "$source: invalid YAML (${e.message?.lineSequence()?.firstOrNull()})"
            } ?: return null to "$source: expected a mapping at the top level"

            fun fail(msg: String) = null to "$source: $msg"
            val id = root["id"]?.toString().orEmpty()
            if (!ID.matches(id)) return fail("id must match ${ID.pattern}")
            val level = (root["level"] as? Number)?.toInt() ?: 1
            val title = l10n(root["title"]) ?: return fail("missing title")
            val summary = l10n(root["summary"]) ?: return fail("missing summary")
            val steps = (root["steps"] as? List<*>)?.map { l10n(it) ?: return fail("a step has no text") }.orEmpty()
            if (steps.isEmpty()) return fail("needs at least one step")
            val hints = (root["hints"] as? List<*>)?.map { l10n(it) ?: return fail("a hint has no text") }.orEmpty()
            val solution = (root["solution"] as? List<*>)?.map { s ->
                val m = s as? Map<*, *> ?: return fail("solution entries need a code block")
                SnippetDto(l10n(m["title"]), m["code"]?.toString()?.trimEnd() ?: return fail("solution entry without code"))
            }.orEmpty()

            val rawChecks = root["checks"] as? List<*> ?: return fail("missing checks")
            if (rawChecks.isEmpty()) return fail("needs at least one check")
            val checks = mutableListOf<CheckSpec>()
            val checkDtos = mutableListOf<LabCheckDto>()
            rawChecks.forEachIndexed { i, raw ->
                val m = raw as? Map<*, *> ?: return fail("check ${i + 1} is not a mapping")
                val type = m["type"]?.toString().orEmpty()
                val required = REQUIRED[type] ?: return fail("check ${i + 1}: unknown type '$type'")
                val params = m.entries.associate { it.key.toString() to it.value }
                required.firstOrNull { params[it].toString().isBlank() || params[it] == null }?.let { return fail("check ${i + 1} ($type): missing '$it'") }
                params.filterKeys { it in NAMED }.entries.firstOrNull { !NAME.matches(it.value.toString()) }
                    ?.let { return fail("check ${i + 1} ($type): '${it.key}' has an invalid value") }
                checkDtos += LabCheckDto(l10n(m["label"]) ?: return fail("check ${i + 1} has no label"))
                checks += CheckSpec(type, params)
            }
            val stack = root["stack"]?.toString()
            return LabDef(LabDto(id, level, stack, title, summary, steps, hints, solution, checkDtos), checks) to null
        }

        private fun l10n(value: Any?): L10nDto? = when (value) {
            is String -> if (value.isBlank()) null else L10nDto(value, value)
            is Map<*, *> -> {
                val en = value["en"]?.toString()?.takeIf { it.isNotBlank() }
                val es = value["es"]?.toString()?.takeIf { it.isNotBlank() }
                if (en == null && es == null) null else L10nDto(en ?: es!!, es ?: en!!)
            }
            else -> null
        }

        /** Every `*.yml` of the directory, ordered by level then id. A broken file is reported, never fatal. */
        fun load(env: Map<String, String> = System.getenv()): LabCatalog {
            val dir = listOfNotNull(env["NIMBO_LABS_DIR"], "labs", "../labs").map(::File).firstOrNull { it.isDirectory }
                ?: return LabCatalog(emptyList())
            val labs = mutableListOf<LabDef>()
            val problems = mutableListOf<String>()
            dir.listFiles { f -> f.isFile && f.extension == "yml" }?.sortedBy { it.name }?.forEach { file ->
                val (lab, problem) = parseOne(file.readText(), file.name)
                if (lab != null) labs += lab
                if (problem != null) problems += problem
            }
            val ids = labs.groupBy { it.dto.id }.filterValues { it.size > 1 }.keys
            ids.forEach { problems += "duplicate lab id '$it'" }
            return LabCatalog(labs.distinctBy { it.dto.id }.sortedWith(compareBy({ it.dto.level }, { it.dto.id })), problems)
        }
    }
}

/** Evaluates a lab's checks against the live emulators, using the same read-only views as the inspector. */
class LabVerifier(
    private val inspector: Inspector,
    private val chaos: Chaos,
    private val kube: Kube? = null,
    private val traces: Traces? = null,
) {

    suspend fun verify(lab: LabDef): LabVerifyDto {
        val results = lab.checks.mapIndexed { i, spec ->
            val outcome = try {
                evaluate(spec)
            } catch (e: EmulatorUnreachable) {
                "the emulator is not reachable; start it first"
            } catch (e: EmulatorError) {
                if (e.status == 404) "not found" else e.message ?: "emulator error"
            } catch (e: Exception) {
                e.message ?: e::class.simpleName.orEmpty()
            }
            LabCheckResultDto(i, outcome == null, outcome)
        }
        return LabVerifyDto(lab.dto.id, results.all { it.ok }, results)
    }

    /** Returns null when the check passes, otherwise why it does not. */
    private suspend fun evaluate(c: CheckSpec): String? {
        val project = c.str("project") ?: "floci-local"
        return when (c.type) {
            "s3.bucket" -> {
                val names = inspector.buckets().map { it.name }
                if (c.str("name") in names) null else "no such bucket; found: ${names.take(5).ifEmpty { listOf("none") }.joinToString()}"
            }
            "s3.object" -> {
                val text = inspector.preview(c.str("bucket")!!, c.str("key")!!).text
                c.str("contains")?.let { if (it in text) null else "the object does not contain '$it'" }
            }
            "sqs.queue" -> {
                val names = inspector.queues().map { it.name }
                if (c.str("name") in names) null else "no such queue; found: ${names.take(5).ifEmpty { listOf("none") }.joinToString()}"
            }
            "sqs.messages" -> {
                val bodies = inspector.messages(c.str("queue")!!).messages.map { it.body }
                val atLeast = c.int("atLeast", 1)
                val needle = c.str("contains")
                val matching = if (needle == null) bodies else bodies.filter { needle in it }
                if (matching.size >= atLeast) null
                else "found ${matching.size} message(s)${needle?.let { " containing '$it'" }.orEmpty()}, need $atLeast"
            }
            "dynamodb.table" -> {
                val table = inspector.tables().firstOrNull { it.name == c.str("name") } ?: return "no such table"
                c.str("key")?.let { if (it in table.keys) null else "the key is ${table.keys.joinToString()}, not '$it'" }
            }
            "dynamodb.items" -> {
                val items = inspector.items(c.str("table")!!, limit = 100).items
                val atLeast = c.int("atLeast", 1)
                val attr = c.str("attribute")
                val count = if (attr == null) items.size else items.count { (it as? JsonObject)?.containsKey(attr) == true }
                if (count >= atLeast) null else "found $count item(s)${attr?.let { " with '$it'" }.orEmpty()}, need $atLeast"
            }
            "pubsub.topic" -> {
                val names = inspector.pubsub(project).topics.map { it.name }
                if (c.str("name") in names) null else "no such topic; found: ${names.take(5).ifEmpty { listOf("none") }.joinToString()}"
            }
            "pubsub.subscription" -> {
                val sub = inspector.pubsub(project).subscriptions.firstOrNull { it.name == c.str("name") } ?: return "no such subscription"
                c.str("topic")?.let { if (sub.topic == it) null else "it listens to '${sub.topic}', not '$it'" }
            }
            "chaos.fault" -> {
                val view = chaos.view()
                if (!view.available) return "Toxiproxy is not running"
                val target = view.targets.firstOrNull { it.id == c.str("target") } ?: return "unknown target"
                if (!target.enabled && c.str("preset") == "outage") return null
                val toxic = target.toxics.firstOrNull { it.name == c.str("preset") } ?: return "no '${c.str("preset")}' fault is active on ${target.name}"
                val attr = c.str("attribute")
                val min = (c.params["atLeast"] as? Number)?.toDouble()
                if (attr != null && min != null) {
                    val value = toxic.attributes[attr]?.jsonPrimitive?.content?.toDoubleOrNull() ?: return "the fault has no '$attr'"
                    if (value < min) return "$attr is ${value.toLong()}, need at least ${min.toLong()}"
                }
                null
            }
            "k8s.deployment" -> {
                val cluster = kube?.overview() ?: return "Kubernetes is not available"
                if (!cluster.available) return cluster.reason ?: "the cluster is not running"
                val ns = c.str("namespace") ?: "default"
                val d = cluster.deployments.firstOrNull { it.namespace == ns && it.name == c.str("name") } ?: return "no deployment ${c.str("name")} in namespace $ns"
                val atLeast = c.int("atLeast", 1)
                if (d.ready >= atLeast) null else "${d.ready} of ${d.desired} replicas ready, need $atLeast"
            }
            "k8s.service" -> {
                val cluster = kube?.overview() ?: return "Kubernetes is not available"
                if (!cluster.available) return cluster.reason ?: "the cluster is not running"
                val ns = c.str("namespace") ?: "default"
                val s = cluster.services.firstOrNull { it.namespace == ns && it.name == c.str("name") } ?: return "no service ${c.str("name")} in namespace $ns"
                c.str("serviceType")?.let { if (s.type == it) null else "the service is ${s.type}, not $it" }
            }
            "trace.service" -> {
                val names = (traces ?: return "Tracing is not available").services()
                if (c.str("name") in names) null else "no traces from that service yet; found: ${names.take(5).ifEmpty { listOf("none") }.joinToString()}"
            }
            else -> "unsupported check type"
        }
    }
}
