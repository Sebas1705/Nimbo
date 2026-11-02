package es.sebas1705.nimbo

import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.application.Application
import io.ktor.server.application.ApplicationCallPipeline
import io.ktor.server.application.call
import io.ktor.server.application.install
import io.ktor.server.plugins.contentnegotiation.ContentNegotiation
import io.ktor.server.plugins.statuspages.StatusPages
import io.ktor.server.request.host
import io.ktor.server.request.path
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.delete
import io.ktor.server.routing.route
import io.ktor.server.routing.routing
import io.ktor.server.http.content.staticFiles
import io.ktor.server.http.content.staticResources
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.File
import java.security.MessageDigest

data class NimboConfig(
    val port: Int = 8080,
    val apiToken: String? = null,
    val webDir: File? = null,
    val hostEnv: Map<String, String> = emptyMap(),
    /** Extra Host header values accepted besides loopback (e.g. behind a reverse proxy you control). */
    val allowedHosts: Set<String> = emptySet(),
    /** Address to listen on. Anything but loopback is only sensible inside a container whose port you publish on loopback. */
    val host: String = "127.0.0.1",
    /** Where the emulators answer from Nimbo's point of view (host.docker.internal when Nimbo itself runs in Docker). */
    val emulatorHost: String = "localhost",
    /** Classpath folder holding a bundled UI, used when there is no web directory on disk. */
    val webResources: String? = null,
    val version: String? = null,
    /** How Nimbo was installed: "jar" (java -jar), "desktop" (Nimbo.exe) or "docker". The updater acts accordingly. */
    val distribution: String = "jar",
) {
    companion object {
        fun fromEnv(env: Map<String, String> = System.getenv()): NimboConfig {
            val config = build(env)
            val bundled = config.webDir == null && NimboConfig::class.java.getResource("/static/index.html") != null
            return if (bundled) config.copy(webResources = "static") else config
        }

        private fun build(env: Map<String, String>) = NimboConfig(
            port = env["NIMBO_PORT"]?.toIntOrNull() ?: 8080,
            apiToken = env["NIMBO_API_TOKEN"]?.takeIf { it.isNotBlank() },
            // `gradlew :backend:run` starts in backend/, so also look one level up.
            webDir = listOfNotNull(env["NIMBO_WEB_DIR"], "web/dist", "../web/dist")
                .map(::File)
                .firstOrNull { it.isDirectory },
            hostEnv = env,
            host = env["NIMBO_HOST"]?.takeIf { it.isNotBlank() } ?: "127.0.0.1",
            emulatorHost = env["NIMBO_EMULATOR_HOST"]?.takeIf { it.isNotBlank() } ?: "localhost",
            version = NimboConfig::class.java.`package`?.implementationVersion,
            distribution = env["NIMBO_DISTRIBUTION"]?.takeIf { it in setOf("jar", "desktop", "docker") } ?: "jar",
            allowedHosts = env["NIMBO_ALLOWED_HOSTS"].orEmpty().split(',').map { it.trim().lowercase() }.filter { it.isNotEmpty() }.toSet(),
        )
    }
}

private val LOOPBACK_HOSTS = setOf("localhost", "127.0.0.1")

fun Application.nimboModule(
    config: NimboConfig,
    runtime: ContainerRuntime,
    stacks: StackCatalog = StackCatalog.builtIn,
    inspector: Inspector = Inspector(JavaHttpGateway()),
    chaos: Chaos = Chaos(JavaHttpGateway()),
    conformance: Conformance = Conformance(JavaHttpGateway()),
    fidelity: FidelityStore = FidelityStore(),
    labs: LabCatalog = LabCatalog(emptyList()),
    kube: Kube = Kube(ProcessShell()),
    traces: Traces = Traces(JavaHttpGateway()),
    data: AppData = AppData(),
    secrets: SecretStore = UnavailableSecretStore("Secrets can only be protected on Windows; elsewhere use environment variables or a .env file."),
    /** Null where updating makes no sense (tests): the routes are then not mounted. */
    updater: Updater? = null,
    images: Images? = null,
    /** How often the uptime of running providers is sampled for the statistics. */
    sampleSeconds: Long = 15,
) {
    install(ContentNegotiation) { json() }
    // Anti DNS-rebinding: a web page cannot make the browser talk to us under its own hostname.
    intercept(ApplicationCallPipeline.Plugins) {
        // request.host() reads the Host header and falls back to the local address when a client omits it.
        val host = call.request.host().lowercase()
        if (host !in LOOPBACK_HOSTS && host !in config.allowedHosts) {
            call.respond(HttpStatusCode.Forbidden, ErrorDto("Host not allowed"))
            finish()
        }
    }
    install(StatusPages) {
        exception<KubeError> { call, cause -> call.respond(HttpStatusCode.UnprocessableEntity, ErrorDto(cause.message ?: "kubectl failed")) }
        exception<InvalidParam> { call, cause -> call.respond(HttpStatusCode.BadRequest, ErrorDto(cause.message ?: "Invalid parameter")) }
        exception<UnknownTarget> { call, cause -> call.respond(HttpStatusCode.NotFound, ErrorDto(cause.message ?: "Unknown target")) }
        exception<InvalidName> { call, cause -> call.respond(HttpStatusCode.BadRequest, ErrorDto(cause.message ?: "Invalid name")) }
        exception<EmulatorUnreachable> { call, cause -> call.respond(HttpStatusCode.ServiceUnavailable, ErrorDto(cause.message ?: "Emulator not reachable")) }
        exception<EmulatorError> { call, cause ->
            val status = if (cause.status == 404) HttpStatusCode.NotFound else HttpStatusCode.BadGateway
            call.respond(status, ErrorDto(cause.message ?: "Emulator error"))
        }
        exception<io.ktor.server.plugins.BadRequestException> { call, _ -> call.respond(HttpStatusCode.BadRequest, ErrorDto("The request body is missing or not valid JSON for this call")) }
        exception<Throwable> { call, cause ->
            call.application.environment.log.error("Unhandled error on ${call.request.path()}", cause)
            call.respond(HttpStatusCode.InternalServerError, ErrorDto("Internal error"))
        }
    }

    // Time spent up is measured while Nimbo is open, by looking at what runs every few seconds.
    launch {
        while (isActive) {
            delay(sampleSeconds * 1000)
            runCatching { data.stats.sample(runtime.containers().filterValues { it.running }.keys, sampleSeconds) }
        }
    }

    routing {
        route("/api") {
            if (config.apiToken != null) {
                intercept(ApplicationCallPipeline.Plugins) {
                    if (call.request.path() == "/api/health") return@intercept
                    val sent = call.request.headers[HttpHeaders.Authorization]?.removePrefix("Bearer ")?.trim().orEmpty()
                    val ok = MessageDigest.isEqual(sent.toByteArray(), config.apiToken.toByteArray())
                    if (!ok) {
                        call.respond(HttpStatusCode.Unauthorized, ErrorDto("Invalid or missing API token"))
                        finish()
                    }
                }
            }

            get("/health") {
                call.respond(HealthDto("ok", authRequired = config.apiToken != null, version = config.version))
            }
            providerRoutes(config, runtime, data, secrets)
            stackRoutes(config, runtime, stacks, data, secrets)
            inspectRoutes(inspector)
            chaosRoutes(chaos, data.stats)
            fidelityRoutes(conformance, fidelity, data)
            labRoutes(labs, LabVerifier(inspector, chaos, kube, traces), data)
            kubeRoutes(kube)
            traceRoutes(traces)
            dataRoutes(data, secrets, config)
            if (updater != null) updateRoutes(updater)
            if (images != null) imageRoutes(images)
        }

        if (config.webDir != null) {
            staticFiles("/", config.webDir) { default("index.html") }
        } else if (config.webResources != null) {
            staticResources("/", config.webResources) { default("index.html") }
        }
    }
}

