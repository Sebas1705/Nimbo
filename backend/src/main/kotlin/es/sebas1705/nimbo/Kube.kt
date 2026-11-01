package es.sebas1705.nimbo

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.util.concurrent.TimeUnit

class KubeError(message: String) : Exception(message)

/** Like [Exec] but with optional standard input, which `kubectl apply -f -` needs. */
fun interface Shell {
    suspend fun run(args: List<String>, stdin: String?, timeoutSeconds: Long): ExecResult
}

class ProcessShell(private val bin: String = "docker") : Shell {
    override suspend fun run(args: List<String>, stdin: String?, timeoutSeconds: Long): ExecResult = withContext(Dispatchers.IO) {
        try {
            val process = ProcessBuilder(listOf(bin) + args).redirectErrorStream(true).start()
            val writer = Thread {
                runCatching { process.outputStream.use { out -> stdin?.let { out.write(it.toByteArray()) } } }
            }.also { it.start() }
            val text = StringBuilder()
            val pump = Thread { process.inputStream.bufferedReader().forEachLine { text.appendLine(it) } }.also { it.start() }
            if (!process.waitFor(timeoutSeconds, TimeUnit.SECONDS)) {
                process.destroyForcibly()
                return@withContext ExecResult(124, "timed out after ${timeoutSeconds}s")
            }
            pump.join(2_000)
            writer.join(1_000)
            ExecResult(process.exitValue(), text.toString().trim())
        } catch (e: Exception) {
            ExecResult(127, e.message ?: "could not run $bin")
        }
    }
}

@Serializable data class KubeNodeDto(val name: String, val ready: Boolean, val version: String?, val cpu: String?, val memory: String?)
@Serializable data class KubePodDto(val namespace: String, val name: String, val status: String, val ready: String, val restarts: Int, val node: String?, val startedAt: String?)
@Serializable data class KubeDeploymentDto(val namespace: String, val name: String, val ready: Int, val desired: Int)
@Serializable data class KubeServiceDto(val namespace: String, val name: String, val type: String, val clusterIp: String?, val ports: List<String>, val nodePorts: List<Int>)
@Serializable
data class KubeOverviewDto(
    val available: Boolean,
    val reason: String? = null,
    val version: String? = null,
    val namespaces: List<String> = emptyList(),
    val nodes: List<KubeNodeDto> = emptyList(),
    val deployments: List<KubeDeploymentDto> = emptyList(),
    val pods: List<KubePodDto> = emptyList(),
    val services: List<KubeServiceDto> = emptyList(),
)
@Serializable data class ApplyManifestDto(val manifest: String)
@Serializable data class OutputDto(val output: String)
@Serializable data class ScaleDto(val replicas: Int)
@Serializable data class KubeSampleDto(val id: String, val title: String, val description: String, val manifest: String)
@Serializable data class KubeconfigDto(val filename: String, val content: String)

/**
 * A single-node k3s cluster running as the `nimbo-k3s` container. `kubectl` lives inside that container, so
 * nothing needs to be installed on the host and every command is an argument list, never a shell string.
 */
class Kube(private val shell: Shell, private val container: String = "nimbo-k3s") {
    private val json = Json { ignoreUnknownKeys = true }

    private suspend fun kubectl(vararg args: String, stdin: String? = null, timeout: Long = 30): ExecResult {
        val command = buildList {
            add("exec")
            if (stdin != null) add("-i")
            add(container)
            add("kubectl")
            addAll(args)
        }
        return shell.run(command, stdin, timeout)
    }

    suspend fun overview(): KubeOverviewDto {
        val ready = kubectl("get", "--raw=/readyz", timeout = 15)
        if (!ready.ok) {
            val reason = if ("No such container" in ready.output || "is not running" in ready.output) "The k3s container is not running" else "The cluster is still starting"
            return KubeOverviewDto(available = false, reason = reason)
        }
        val all = kubectl("get", "nodes,namespaces,deployments,pods,services", "-A", "-o", "json")
        if (!all.ok) throw KubeError("kubectl get failed: ${all.output.take(300)}")
        val items = json.parseToJsonElement(all.output).jsonObject["items"]?.jsonArray.orEmpty().map { it.jsonObject }
        fun of(kind: String) = items.filter { it["kind"]?.jsonPrimitive?.content == kind }

        val nodes = of("Node").map { n ->
            val status = n["status"]?.jsonObject
            KubeNodeDto(
                name = n.name(),
                ready = status?.get("conditions")?.jsonArray?.any { it.jsonObject.str("type") == "Ready" && it.jsonObject.str("status") == "True" } == true,
                version = status?.get("nodeInfo")?.jsonObject?.str("kubeletVersion"),
                cpu = status?.get("capacity")?.jsonObject?.str("cpu"),
                memory = status?.get("capacity")?.jsonObject?.str("memory"),
            )
        }
        return KubeOverviewDto(
            available = true,
            version = nodes.firstOrNull()?.version,
            namespaces = of("Namespace").map { it.name() }.sorted(),
            nodes = nodes,
            deployments = of("Deployment").map { d ->
                KubeDeploymentDto(d.namespace(), d.name(), d["status"]?.jsonObject?.get("readyReplicas")?.jsonPrimitive?.intOrNull ?: 0, d["spec"]?.jsonObject?.get("replicas")?.jsonPrimitive?.intOrNull ?: 1)
            },
            pods = of("Pod").map(::pod),
            services = of("Service").map(::service),
        )
    }

    suspend fun apply(manifest: String): OutputDto {
        if (manifest.isBlank()) throw InvalidName("The manifest is empty")
        if (manifest.length > MAX_MANIFEST) throw InvalidName("The manifest is larger than ${MAX_MANIFEST / 1024} KB")
        val result = kubectl("apply", "-f", "-", stdin = manifest, timeout = 60)
        if (!result.ok) throw KubeError(result.output.take(800))
        return OutputDto(result.output)
    }

    suspend fun scale(namespace: String, deployment: String, replicas: Int): OutputDto {
        validate(namespace, deployment)
        if (replicas !in 0..MAX_REPLICAS) throw InvalidName("replicas must be between 0 and $MAX_REPLICAS")
        return run("scale", "deployment/$deployment", "-n", namespace, "--replicas=$replicas")
    }

    suspend fun delete(kind: String, namespace: String, name: String): OutputDto {
        if (kind !in DELETABLE) throw InvalidName("kind must be one of ${DELETABLE.joinToString()}")
        validate(namespace, name)
        if (namespace in PROTECTED) throw InvalidName("Resources in $namespace cannot be deleted from here")
        return run("delete", kind, name, "-n", namespace)
    }

    suspend fun logs(namespace: String, pod: String, tail: Int = 200): OutputDto {
        validate(namespace, pod)
        val result = kubectl("logs", "--tail=${tail.coerceIn(1, 2000)}", pod, "-n", namespace, "--all-containers=true")
        if (!result.ok) throw KubeError(result.output.take(500))
        return OutputDto(result.output)
    }

    suspend fun kubeconfig(): KubeconfigDto {
        val result = shell.run(listOf("exec", container, "cat", "/etc/rancher/k3s/k3s.yaml"), null, 15)
        if (!result.ok) throw KubeError("The k3s container is not running")
        return KubeconfigDto("nimbo-k3s.kubeconfig.yaml", result.output)
    }

    private suspend fun run(vararg args: String): OutputDto {
        val result = kubectl(*args)
        if (!result.ok) throw KubeError(result.output.take(500))
        return OutputDto(result.output)
    }

    private fun validate(namespace: String, name: String) {
        if (!NS.matches(namespace)) throw InvalidName("Invalid namespace")
        if (!NAME.matches(name)) throw InvalidName("Invalid name")
    }

    // ---- JSON helpers ----

    private fun JsonObject.str(key: String) = get(key)?.jsonPrimitive?.contentOrNull
    private fun JsonObject.meta() = get("metadata")?.jsonObject ?: JsonObject(emptyMap())
    private fun JsonObject.name() = meta().str("name").orEmpty()
    private fun JsonObject.namespace() = meta().str("namespace").orEmpty()

    private fun pod(p: JsonObject): KubePodDto {
        val status = p["status"]?.jsonObject ?: JsonObject(emptyMap())
        val containers = status["containerStatuses"]?.jsonArray?.map { it.jsonObject }.orEmpty()
        val total = containers.size.takeIf { it > 0 } ?: p["spec"]?.jsonObject?.get("containers")?.jsonArray?.size ?: 0
        val waiting = containers.firstNotNullOfOrNull { it["state"]?.jsonObject?.get("waiting")?.jsonObject?.str("reason") }
        val terminated = containers.firstNotNullOfOrNull { it["state"]?.jsonObject?.get("terminated")?.jsonObject?.str("reason") }
        // Between two restarts a crashing container is briefly "running"; kubectl shows that as Error, so do we.
        val flapping = containers.any {
            it["ready"]?.jsonPrimitive?.contentOrNull != "true" && (it["restartCount"]?.jsonPrimitive?.intOrNull ?: 0) > 0 &&
                it["lastState"]?.jsonObject?.get("terminated") != null
        }
        val phase = when {
            p.meta()["deletionTimestamp"] != null -> "Terminating"
            waiting != null -> waiting
            terminated != null -> terminated
            flapping -> "Error"
            else -> status.str("phase") ?: "Unknown"
        }
        return KubePodDto(
            namespace = p.namespace(),
            name = p.name(),
            status = phase,
            ready = "${containers.count { it["ready"]?.jsonPrimitive?.contentOrNull == "true" }}/$total",
            restarts = containers.sumOf { it["restartCount"]?.jsonPrimitive?.intOrNull ?: 0 },
            node = p["spec"]?.jsonObject?.str("nodeName"),
            startedAt = status.str("startTime"),
        )
    }

    private fun service(s: JsonObject): KubeServiceDto {
        val spec = s["spec"]?.jsonObject ?: JsonObject(emptyMap())
        val ports = (spec["ports"] as? JsonArray).orEmpty().map { it.jsonObject }
        return KubeServiceDto(
            namespace = s.namespace(),
            name = s.name(),
            type = spec.str("type") ?: "ClusterIP",
            clusterIp = spec.str("clusterIP"),
            ports = ports.map { p ->
                val node = p["nodePort"]?.jsonPrimitive?.intOrNull
                "${p.str("port")}${if (node != null) ":$node" else ""}/${p.str("protocol") ?: "TCP"}"
            },
            nodePorts = ports.mapNotNull { it["nodePort"]?.jsonPrimitive?.intOrNull },
        )
    }

    companion object {
        const val MAX_MANIFEST = 256 * 1024
        const val MAX_REPLICAS = 20
        private val NAME = Regex("[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?")
        private val NS = Regex("[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?")
        private val DELETABLE = listOf("pod", "deployment", "service")
        private val PROTECTED = setOf("kube-system", "kube-public", "kube-node-lease")

        val samples = listOf(
            KubeSampleDto(
                "hello", "Hello (nginx)", "Two nginx replicas behind a NodePort service, reachable at http://localhost:30080.",
                """
                apiVersion: apps/v1
                kind: Deployment
                metadata:
                  name: hello
                  labels: {app: hello}
                spec:
                  replicas: 2
                  selector:
                    matchLabels: {app: hello}
                  template:
                    metadata:
                      labels: {app: hello}
                    spec:
                      containers:
                        - name: web
                          image: nginx:alpine
                          ports:
                            - containerPort: 80
                ---
                apiVersion: v1
                kind: Service
                metadata:
                  name: hello
                spec:
                  type: NodePort
                  selector: {app: hello}
                  ports:
                    - port: 80
                      targetPort: 80
                      nodePort: 30080
                """.trimIndent() + "\n",
            ),
            KubeSampleDto(
                "crashloop", "Crash loop", "A container that always exits: watch the status go to CrashLoopBackOff and read its logs.",
                """
                apiVersion: apps/v1
                kind: Deployment
                metadata:
                  name: crashy
                spec:
                  replicas: 1
                  selector:
                    matchLabels: {app: crashy}
                  template:
                    metadata:
                      labels: {app: crashy}
                    spec:
                      containers:
                        - name: boom
                          image: busybox:stable
                          command: ["sh", "-c", "echo starting; echo 'fatal: config missing' >&2; exit 1"]
                """.trimIndent() + "\n",
            ),
        )
    }
}

private fun JsonArray?.orEmpty(): JsonArray = this ?: JsonArray(emptyList<JsonPrimitive>())
