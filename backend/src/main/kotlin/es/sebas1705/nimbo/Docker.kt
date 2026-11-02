package es.sebas1705.nimbo

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.TimeUnit

data class ExecResult(val exitCode: Int, val output: String) {
    val ok get() = exitCode == 0
}

fun interface Exec {
    suspend fun run(args: List<String>, timeoutSeconds: Long): ExecResult
}

class ProcessExec(private val bin: String = "docker") : Exec {
    override suspend fun run(args: List<String>, timeoutSeconds: Long): ExecResult = withContext(Dispatchers.IO) {
        try {
            val process = ProcessBuilder(listOf(bin) + args).redirectErrorStream(true).start()
            val reader = process.inputStream.bufferedReader()
            val text = StringBuilder()
            val pump = Thread { reader.forEachLine { text.appendLine(it) } }.also { it.start() }
            if (!process.waitFor(timeoutSeconds, TimeUnit.SECONDS)) {
                process.destroyForcibly()
                return@withContext ExecResult(124, "timed out after ${timeoutSeconds}s")
            }
            pump.join(2_000)
            ExecResult(process.exitValue(), text.toString().trim())
        } catch (e: Exception) {
            ExecResult(127, e.message ?: "could not run $bin")
        }
    }
}

/** [persist] is how the container was created: with its data in a volume, or without. */
data class ContainerInfo(val providerId: String, val running: Boolean, val status: String, val persist: Boolean = false)

interface ContainerRuntime {
    suspend fun status(): DockerStatusDto
    suspend fun containers(): Map<String, ContainerInfo>
    suspend fun start(def: ProviderDef, hostEnv: Map<String, String>): Result<Unit>
    suspend fun stop(def: ProviderDef): Result<Unit>
    suspend fun remove(def: ProviderDef): Result<Unit>
    suspend fun logs(def: ProviderDef, tail: Int): Result<String>

    /** Starts keeping the provider's data in a volume (or not). Runtimes without volumes ignore the flag. */
    suspend fun start(def: ProviderDef, hostEnv: Map<String, String>, persist: Boolean): Result<Unit> = start(def, hostEnv)

    /** Ids of the providers that have saved data in a volume. */
    suspend fun savedData(): Set<String> = emptySet()

    /** Deletes the saved data of a provider (and its stopped container). */
    suspend fun deleteData(def: ProviderDef): Result<Unit> = Result.success(Unit)
}

/** Drives the local `docker` CLI. Only ever touches containers labelled `nimbo.managed=true`. */
class CliContainerRuntime(private val exec: Exec = ProcessExec()) : ContainerRuntime {

    override suspend fun status(): DockerStatusDto {
        val r = exec.run(listOf("info", "--format", "{{.ServerVersion}}"), 15)
        return if (r.ok) DockerStatusDto(true, version = r.output.lines().lastOrNull()?.trim())
        else DockerStatusDto(false, error = "Docker daemon not reachable")
    }

    override suspend fun containers(): Map<String, ContainerInfo> {
        val r = exec.run(
            listOf(
                "ps", "-a", "--filter", "label=$LABEL_MANAGED=true",
                // No quotes in the template: ProcessBuilder on Windows does not escape them for docker.exe.
                "--format", "{{.Names}}|{{.State}}|{{.Status}}|{{.Labels}}",
            ),
            15,
        )
        if (!r.ok) return emptyMap()
        return r.output.lines().mapNotNull { line ->
            val parts = line.split('|', limit = 4)
            val id = parts.getOrNull(0)?.removePrefix(NAME_PREFIX)
            if (parts.size < 3 || id.isNullOrBlank()) null
            else id to ContainerInfo(id, parts[1] == "running", parts[2], persist = parts.getOrNull(3)?.contains("$LABEL_PERSIST=true") == true)
        }.toMap()
    }

    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>): Result<Unit> = start(def, hostEnv, persist = false)

    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>, persist: Boolean): Result<Unit> {
        val missing = def.secretEnv.values.filter { hostEnv[it].isNullOrBlank() }
        if (missing.isNotEmpty()) {
            return Result.failure(
                IllegalStateException("Missing secrets: ${missing.joinToString()}. Set them in Nimbo's environment (a .env file with docker compose, or 'doppler run --')."),
            )
        }
        val wanted = persist && def.persist != null
        val existing = containers()[def.id]
        if (existing != null && (existing.persist == wanted || existing.running)) return run(listOf("start", containerName(def)))
        // A stopped container created in the other mode is replaced; the volume, if any, is left alone.
        if (existing != null) run(listOf("rm", "-f", containerName(def)))
        return run(buildRunArgs(def, hostEnv, wanted))
    }

    override suspend fun savedData(): Set<String> {
        val r = exec.run(listOf("volume", "ls", "--filter", "name=$NAME_PREFIX", "--format", "{{.Name}}"), 15)
        if (!r.ok) return emptySet()
        return r.output.lines().map { it.trim() }.filter { it.startsWith(NAME_PREFIX) && it.endsWith(VOLUME_SUFFIX) }
            .map { it.removePrefix(NAME_PREFIX).removeSuffix(VOLUME_SUFFIX) }.toSet()
    }

    override suspend fun deleteData(def: ProviderDef): Result<Unit> {
        val existing = containers()[def.id]
        if (existing?.running == true) return Result.failure(IllegalStateException("Stop ${def.name} before deleting its data"))
        if (existing != null) run(listOf("rm", "-f", containerName(def)))
        val r = exec.run(listOf("volume", "rm", volumeName(def)), 30)
        return if (r.ok || "No such volume" in r.output) Result.success(Unit) else Result.failure(IllegalStateException(r.output.ifBlank { "docker failed" }))
    }

    override suspend fun stop(def: ProviderDef): Result<Unit> = run(listOf("stop", containerName(def)))

    override suspend fun remove(def: ProviderDef): Result<Unit> = run(listOf("rm", "-f", containerName(def)))

    override suspend fun logs(def: ProviderDef, tail: Int): Result<String> {
        val r = exec.run(listOf("logs", "--tail", tail.coerceIn(1, 2000).toString(), containerName(def)), 15)
        return if (r.ok) Result.success(r.output) else Result.failure(IllegalStateException(r.output))
    }

    private suspend fun run(args: List<String>): Result<Unit> {
        val r = exec.run(args, 120)
        return if (r.ok) Result.success(Unit) else Result.failure(IllegalStateException(r.output.ifBlank { "docker failed" }))
    }

    companion object {
        const val LABEL_MANAGED = "nimbo.managed"
        const val LABEL_PROVIDER = "nimbo.provider"
        const val LABEL_PERSIST = "nimbo.persist"

        const val NAME_PREFIX = "nimbo-"
        const val VOLUME_SUFFIX = "-data"

        fun containerName(def: ProviderDef) = "$NAME_PREFIX${def.id}"

        /** One named volume per provider, so deleting a container never deletes what it saved. */
        fun volumeName(def: ProviderDef) = "$NAME_PREFIX${def.id}$VOLUME_SUFFIX"

        fun buildRunArgs(def: ProviderDef, hostEnv: Map<String, String>, persist: Boolean = false): List<String> = buildList {
            val keep = if (persist) def.persist else null
            addAll(listOf("run", "-d", "--name", containerName(def)))
            addAll(listOf("--label", "$LABEL_MANAGED=true", "--label", "$LABEL_PROVIDER=${def.id}", "--label", "$LABEL_PERSIST=${keep != null}"))
            def.ports.forEach { addAll(listOf("-p", "127.0.0.1:$it:$it")) }
            keep?.let { addAll(listOf("-v", "${volumeName(def)}:${it.mountPath}")) }
            keep?.user?.let { addAll(listOf("--user", it)) }
            def.containerEnv.forEach { (k, v) -> addAll(listOf("-e", "$k=$v")) }
            keep?.env?.forEach { (k, v) -> addAll(listOf("-e", "$k=$v")) }
            def.secretEnv.forEach { (container, host) -> addAll(listOf("-e", "$container=${hostEnv.getValue(host)}")) }
            addAll(def.runArgs)
            add(def.image)
            addAll(keep?.command ?: def.command)
        }
    }
}
