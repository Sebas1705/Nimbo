package es.sebas1705.nimbo

import kotlinx.serialization.Serializable

enum class Cloud { AWS, AZURE, GCP, OCI, OTHER }

/** How a provider keeps its data between runs: a named Docker volume mounted at [mountPath]. */
data class PersistSpec(
    val mountPath: String,
    /** Environment that only makes sense when persisting (an emulator storage mode, say). */
    val env: Map<String, String> = emptyMap(),
    /** Replaces the container command when persisting; null keeps the image default. */
    val command: List<String>? = null,
    /** Images that run as an unprivileged user cannot write into a fresh volume (owned by root); this runs them as that user. */
    val user: String? = null,
)

/** A container Nimbo knows how to run. Secrets are never stored here, only the name of the env var that holds them. */
data class ProviderDef(
    val id: String,
    val name: String,
    val cloud: Cloud,
    val description: String,
    val image: String,
    val ports: List<Int>,
    val command: List<String> = emptyList(),
    /** Extra `docker run` flags placed before the image (e.g. --add-host). */
    val runArgs: List<String> = emptyList(),
    /** Container env with fixed, non-secret values. */
    val containerEnv: Map<String, String> = emptyMap(),
    /** Container env whose value is read from Nimbo's own environment (fed by Doppler): container var -> host var. */
    val secretEnv: Map<String, String> = emptyMap(),
    /** Variables a client should set to talk to this provider. */
    val endpoints: Map<String, String> = emptyMap(),
    /** Null when the provider has nothing worth keeping (a proxy, an emulator without persistence). */
    val persist: PersistSpec? = null,
)

enum class RunState { NOT_CREATED, RUNNING, STOPPED, ERROR }

@Serializable
data class ProviderDto(
    val id: String,
    val name: String,
    val cloud: String,
    val description: String,
    val image: String,
    val ports: List<Int>,
    val state: String,
    val detail: String? = null,
    /** Can this provider keep its data in a volume? */
    val persistable: Boolean = false,
    /** Is that switched on? */
    val persist: Boolean = false,
    /** Is there saved data in a volume right now? */
    val hasData: Boolean = false,
)

@Serializable
data class DockerStatusDto(val available: Boolean, val version: String? = null, val error: String? = null)

@Serializable
data class HealthDto(val status: String, val authRequired: Boolean, val version: String? = null)

@Serializable
data class LogsDto(val id: String, val lines: String)

@Serializable
data class EnvDto(val id: String, val variables: Map<String, String>, val dotenv: String)

@Serializable
data class ErrorDto(val error: String)

@Serializable
data class StackDto(
    val id: String,
    val description: String,
    val providers: List<String>,
    /** running (all up), partial, or stopped (none up). */
    val state: String,
    val running: Int,
    val total: Int,
)

@Serializable
data class StackListDto(val stacks: List<StackDto>, val problems: List<String>)

@Serializable
data class StepResultDto(val id: String, val ok: Boolean, val error: String? = null)

@Serializable
data class StackActionDto(val id: String, val action: String, val results: List<StepResultDto>)

@Serializable
data class ExportDto(val format: String, val filename: String, val content: String)
