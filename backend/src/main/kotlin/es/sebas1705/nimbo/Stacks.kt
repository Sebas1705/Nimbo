package es.sebas1705.nimbo

import org.yaml.snakeyaml.LoaderOptions
import org.yaml.snakeyaml.Yaml
import org.yaml.snakeyaml.constructor.SafeConstructor
import java.io.File

/** A named group of providers that start and stop together. */
data class StackDef(val id: String, val description: String, val providers: List<String>)

/** Stacks that loaded, plus human-readable reasons for entries that were skipped. */
class StackCatalog(val stacks: List<StackDef>, val problems: List<String> = emptyList()) {
    fun find(id: String): StackDef? = stacks.firstOrNull { it.id == id }

    companion object {
        private val ID = Regex("[a-z0-9][a-z0-9-]{0,40}")

        val builtIn = StackCatalog(
            listOf(
                StackDef("aws-basic", "AWS emulator plus S3-compatible storage", listOf("floci-aws", "minio")),
                StackDef("multicloud", "AWS, Azure and GCP emulators side by side", listOf("floci-aws", "floci-azure", "floci-gcp")),
                StackDef("event-driven", "Messaging and data stores for event-driven labs", listOf("floci-aws", "redpanda", "postgres", "redis")),
            ),
        )

        fun parse(text: String): StackCatalog {
            val root = try {
                Yaml(SafeConstructor(LoaderOptions())).load<Any?>(text)
            } catch (e: Exception) {
                return StackCatalog(emptyList(), listOf("Invalid YAML: ${e.message?.lineSequence()?.firstOrNull()}"))
            }
            val entries = (root as? Map<*, *>)?.get("stacks") as? Map<*, *>
                ?: return StackCatalog(emptyList(), listOf("Missing top-level 'stacks' map"))

            val problems = mutableListOf<String>()
            val stacks = entries.mapNotNull { (rawId, rawBody) ->
                val id = rawId.toString()
                val body = rawBody as? Map<*, *>
                val providers = (body?.get("providers") as? List<*>)?.map { it.toString() }.orEmpty()
                val unknown = providers.filter { Catalog.find(it) == null }
                val problem = when {
                    !ID.matches(id) -> "Stack '$id': id must match ${ID.pattern}"
                    providers.isEmpty() -> "Stack '$id': 'providers' is empty or missing"
                    unknown.isNotEmpty() -> "Stack '$id': unknown providers ${unknown.joinToString()}"
                    else -> null
                }
                if (problem != null) {
                    problems += problem
                    null
                } else {
                    StackDef(id, body?.get("description")?.toString().orEmpty(), providers.distinct())
                }
            }
            return StackCatalog(stacks, problems)
        }

        /** Looks for NIMBO_STACKS_FILE, then nimbo.yml here and one level up (gradle runs in backend/). */
        fun load(env: Map<String, String> = System.getenv()): StackCatalog {
            val file = listOfNotNull(env["NIMBO_STACKS_FILE"], "nimbo.yml", "../nimbo.yml")
                .map(::File)
                .firstOrNull { it.isFile }
                ?: return builtIn
            return parse(file.readText())
        }
    }
}
