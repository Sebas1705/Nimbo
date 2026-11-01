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

data class ContainerInfo(val providerId: String, val running: Boolean, val status: String)

interface ContainerRuntime {
    suspend fun status(): DockerStatusDto
    suspend fun containers(): Map<String, ContainerInfo>
    suspend fun start(def: ProviderDef, hostEnv: Map<String, String>): Result<Unit>
    suspend fun stop(def: ProviderDef): Result<Unit>
    suspend fun remove(def: ProviderDef): Result<Unit>
    suspend fun logs(def: ProviderDef, tail: Int): Result<String>
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
                "--format", "{{.Names}}|{{.State}}|{{.Status}}",
            ),
            15,
        )
        if (!r.ok) return emptyMap()
        return r.output.lines().mapNotNull { line ->
            val parts = line.split('|', limit = 3)
            val id = parts.getOrNull(0)?.removePrefix(NAME_PREFIX)
            if (parts.size < 3 || id.isNullOrBlank()) null
            else id to ContainerInfo(id, parts[1] == "running", parts[2])
        }.toMap()
    }

    override suspend fun start(def: ProviderDef, hostEnv: Map<String, String>): Result<Unit> {
        val missing = def.secretEnv.values.filter { hostEnv[it].isNullOrBlank() }
        if (missing.isNotEmpty()) {
            return Result.failure(
                IllegalStateException("Missing secrets: ${missing.joinToString()}. Run Nimbo with 'doppler run --'."),
            )
        }
        val existing = containers()[def.id]
        if (existing != null) return run(listOf("start", containerName(def)))
        return run(buildRunArgs(def, hostEnv))
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

        const val NAME_PREFIX = "nimbo-"

        fun containerName(def: ProviderDef) = "$NAME_PREFIX${def.id}"

        fun buildRunArgs(def: ProviderDef, hostEnv: Map<String, String>): List<String> = buildList {
            addAll(listOf("run", "-d", "--name", containerName(def)))
            addAll(listOf("--label", "$LABEL_MANAGED=true", "--label", "$LABEL_PROVIDER=${def.id}"))
            def.ports.forEach { addAll(listOf("-p", "127.0.0.1:$it:$it")) }
            def.containerEnv.forEach { (k, v) -> addAll(listOf("-e", "$k=$v")) }
            def.secretEnv.forEach { (container, host) -> addAll(listOf("-e", "$container=${hostEnv.getValue(host)}")) }
            add(def.image)
            addAll(def.command)
        }
    }
}
