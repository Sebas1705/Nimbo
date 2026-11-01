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
import io.ktor.server.request.path
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.delete
import io.ktor.server.routing.route
import io.ktor.server.routing.routing
import io.ktor.server.http.content.staticFiles
import java.io.File
import java.security.MessageDigest

data class NimboConfig(
    val port: Int = 8080,
    val apiToken: String? = null,
    val webDir: File? = null,
    val hostEnv: Map<String, String> = emptyMap(),
) {
    companion object {
        fun fromEnv(env: Map<String, String> = System.getenv()): NimboConfig = NimboConfig(
            port = env["NIMBO_PORT"]?.toIntOrNull() ?: 8080,
            apiToken = env["NIMBO_API_TOKEN"]?.takeIf { it.isNotBlank() },
            // `gradlew :backend:run` starts in backend/, so also look one level up.
            webDir = listOfNotNull(env["NIMBO_WEB_DIR"], "web/dist", "../web/dist")
                .map(::File)
                .firstOrNull { it.isDirectory },
            hostEnv = env,
        )
    }
}

fun Application.nimboModule(
    config: NimboConfig,
    runtime: ContainerRuntime,
    stacks: StackCatalog = StackCatalog.builtIn,
) {
    install(ContentNegotiation) { json() }
    install(StatusPages) {
        exception<Throwable> { call, cause ->
            call.application.environment.log.error("Unhandled error on ${call.request.path()}", cause)
            call.respond(HttpStatusCode.InternalServerError, ErrorDto("Internal error"))
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
                call.respond(HealthDto("ok", authRequired = config.apiToken != null))
            }
            providerRoutes(config, runtime)
            stackRoutes(config, runtime, stacks)
        }

        config.webDir?.let { dir ->
            staticFiles("/", dir) { default("index.html") }
        }
    }
}

private fun Route.providerRoutes(config: NimboConfig, runtime: ContainerRuntime) {
    get("/docker") { call.respond(runtime.status()) }

    get("/providers") {
        val docker = runtime.status()
        val live = if (docker.available) runtime.containers() else emptyMap()
        call.respond(
            Catalog.providers.map { def ->
                val info = live[def.id]
                val state = when {
                    info == null -> RunState.NOT_CREATED
                    info.running -> RunState.RUNNING
                    else -> RunState.STOPPED
                }
                def.toDto(state, info?.status)
            },
        )
    }

    route("/providers/{id}") {
        post("/start") {
            val def = call.provider() ?: return@post
            runtime.start(def, config.hostEnv)
                .onSuccess { call.respond(HttpStatusCode.Accepted, def.toDto(RunState.RUNNING, null)) }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "start failed")) }
        }
        post("/stop") {
            val def = call.provider() ?: return@post
            runtime.stop(def)
                .onSuccess { call.respond(HttpStatusCode.Accepted, def.toDto(RunState.STOPPED, null)) }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "stop failed")) }
        }
        delete {
            val def = call.provider() ?: return@delete
            runtime.remove(def)
                .onSuccess { call.respond(HttpStatusCode.NoContent) }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "remove failed")) }
        }
        get("/logs") {
            val def = call.provider() ?: return@get
            val tail = call.request.queryParameters["tail"]?.toIntOrNull() ?: 200
            runtime.logs(def, tail)
                .onSuccess { call.respond(LogsDto(def.id, it)) }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "no logs")) }
        }
        get("/env") {
            val def = call.provider() ?: return@get
            call.respond(EnvDto(def.id, def.endpoints, def.endpoints.entries.joinToString("\n") { "${it.key}=${it.value}" }))
        }
    }
}

private fun Route.stackRoutes(config: NimboConfig, runtime: ContainerRuntime, stacks: StackCatalog) {
    get("/stacks") {
        val live = if (runtime.status().available) runtime.containers() else emptyMap()
        call.respond(StackListDto(stacks.stacks.map { it.toDto(live) }, stacks.problems))
    }

    route("/stacks/{id}") {
        post("/up") {
            val stack = call.stack(stacks) ?: return@post
            val live = runtime.containers()
            // Sequential on purpose: parallel `docker run` of several big images is hard to read when one fails.
            val results = stack.providers.map { id ->
                if (live[id]?.running == true) return@map StepResultDto(id, ok = true)
                runtime.start(Catalog.find(id)!!, config.hostEnv).toStep(id)
            }
            call.respond(StackActionDto(stack.id, "up", results))
        }
        post("/down") {
            val stack = call.stack(stacks) ?: return@post
            val live = runtime.containers()
            val results = stack.providers.reversed().map { id ->
                if (live[id]?.running != true) return@map StepResultDto(id, ok = true)
                runtime.stop(Catalog.find(id)!!).toStep(id)
            }
            call.respond(StackActionDto(stack.id, "down", results))
        }
        get("/export") {
            val stack = call.stack(stacks) ?: return@get
            val format = ExportFormat.of(call.request.queryParameters["format"])
                ?: return@get call.respond(HttpStatusCode.BadRequest, ErrorDto("format must be env, tofu or awscli"))
            val defs = stack.providers.mapNotNull(Catalog::find)
            if (format != ExportFormat.ENV && defs.none { it.id == "floci-aws" }) {
                return@get call.respond(HttpStatusCode.Conflict, ErrorDto("Stack '${stack.id}' has no AWS emulator (floci-aws)"))
            }
            val content = when (format) {
                ExportFormat.ENV -> Exports.env(defs)
                ExportFormat.TOFU -> Exports.tofuAwsOverride()
                ExportFormat.AWS_CLI -> Exports.awsCliProfile()
            }
            call.respond(ExportDto(format.id, format.filename, content))
        }
    }
}

private fun Result<Unit>.toStep(id: String) = StepResultDto(id, isSuccess, exceptionOrNull()?.message)

private fun StackDef.toDto(live: Map<String, ContainerInfo>): StackDto {
    val running = providers.count { live[it]?.running == true }
    val state = when (running) {
        0 -> "stopped"
        providers.size -> "running"
        else -> "partial"
    }
    return StackDto(id, description, providers, state, running, providers.size)
}

private suspend fun io.ktor.server.application.ApplicationCall.stack(stacks: StackCatalog): StackDef? {
    val id = parameters["id"].orEmpty()
    val stack = stacks.find(id)
    if (stack == null) respond(HttpStatusCode.NotFound, ErrorDto("Unknown stack '$id'"))
    return stack
}

private suspend fun io.ktor.server.application.ApplicationCall.provider(): ProviderDef? {
    val id = parameters["id"].orEmpty()
    val def = Catalog.find(id)
    if (def == null) respond(HttpStatusCode.NotFound, ErrorDto("Unknown provider '$id'"))
    return def
}

private fun ProviderDef.toDto(state: RunState, detail: String?) =
    ProviderDto(id, name, cloud.name.lowercase(), description, image, ports, state.name.lowercase(), detail)
