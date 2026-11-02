package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.ApplicationCall
import io.ktor.server.application.call
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

/** Start, stop and inspect the providers. Starting uses the saved secrets and the data setting of each provider. */
internal fun Route.providerRoutes(config: NimboConfig, runtime: ContainerRuntime, data: AppData, secrets: SecretStore) {
    get("/docker") { call.respond(runtime.status()) }

    get("/providers") {
        val docker = runtime.status()
        val live = if (docker.available) runtime.containers() else emptyMap()
        val saved = if (docker.available) runtime.savedData() else emptySet()
        val persisting = data.settings.get().persist
        call.respond(
            Catalog.providers.map { def ->
                val info = live[def.id]
                val state = when {
                    info == null -> RunState.NOT_CREATED
                    info.running -> RunState.RUNNING
                    else -> RunState.STOPPED
                }
                def.toDto(state, info?.status, persist = def.id in persisting, hasData = def.id in saved)
            },
        )
    }

    route("/providers/{id}") {
        post("/start") {
            val def = call.provider() ?: return@post
            val env = Secrets.envFor(def, config.hostEnv, secrets)
            runtime.start(def, env, data.settings.persists(def.id))
                .onSuccess {
                    data.stats.record("provider.start", def.id)
                    call.respond(HttpStatusCode.Accepted, def.toDto(RunState.RUNNING, null))
                }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "start failed")) }
        }
        post("/stop") {
            val def = call.provider() ?: return@post
            runtime.stop(def)
                .onSuccess {
                    data.stats.record("provider.stop", def.id)
                    call.respond(HttpStatusCode.Accepted, def.toDto(RunState.STOPPED, null))
                }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "stop failed")) }
        }
        delete {
            val def = call.provider() ?: return@delete
            runtime.remove(def)
                .onSuccess {
                    data.stats.record("provider.remove", def.id)
                    call.respond(HttpStatusCode.NoContent)
                }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "remove failed")) }
        }
        post("/persist") {
            val def = call.provider() ?: return@post
            val enabled = call.receive<PersistDto>().enabled
            val settings = data.settings.setPersist(def.id, enabled)
            data.stats.record("provider.persist", def.id, if (enabled) "on" else "off")
            call.respond(settings)
        }
        delete("/data") {
            val def = call.provider() ?: return@delete
            if (call.request.queryParameters["confirm"] != "yes") {
                return@delete call.respond(HttpStatusCode.BadRequest, ErrorDto("Add confirm=yes: this deletes the saved data of ${def.name} and cannot be undone"))
            }
            runtime.deleteData(def)
                .onSuccess {
                    data.stats.record("provider.data.delete", def.id)
                    call.respond(HttpStatusCode.NoContent)
                }
                .onFailure { call.respond(HttpStatusCode.Conflict, ErrorDto(it.message ?: "could not delete the data")) }
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

internal fun Route.stackRoutes(config: NimboConfig, runtime: ContainerRuntime, stacks: StackCatalog, data: AppData, secrets: SecretStore) {
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
                val def = Catalog.find(id)!!
                runtime.start(def, Secrets.envFor(def, config.hostEnv, secrets), data.settings.persists(id))
                    .also { if (it.isSuccess) data.stats.record("provider.start", id) }
                    .toStep(id)
            }
            data.stats.record("stack.up", stack.id)
            call.respond(StackActionDto(stack.id, "up", results))
        }
        post("/down") {
            val stack = call.stack(stacks) ?: return@post
            val live = runtime.containers()
            val results = stack.providers.reversed().map { id ->
                if (live[id]?.running != true) return@map StepResultDto(id, ok = true)
                runtime.stop(Catalog.find(id)!!).also { if (it.isSuccess) data.stats.record("provider.stop", id) }.toStep(id)
            }
            data.stats.record("stack.down", stack.id)
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
            val chaos = call.request.queryParameters["chaos"] == "true"
            val content = when (format) {
                ExportFormat.ENV -> Exports.env(defs, chaos)
                ExportFormat.TOFU -> Exports.tofuAwsOverride(chaos)
                ExportFormat.AWS_CLI -> Exports.awsCliProfile(chaos)
            }
            data.stats.record("export", stack.id, format.id)
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

private suspend fun ApplicationCall.stack(stacks: StackCatalog): StackDef? {
    val id = parameters["id"].orEmpty()
    val stack = stacks.find(id)
    if (stack == null) respond(HttpStatusCode.NotFound, ErrorDto("Unknown stack '$id'"))
    return stack
}

private suspend fun ApplicationCall.provider(): ProviderDef? {
    val id = parameters["id"].orEmpty()
    val def = Catalog.find(id)
    if (def == null) respond(HttpStatusCode.NotFound, ErrorDto("Unknown provider '$id'"))
    return def
}

private fun ProviderDef.toDto(state: RunState, detail: String?, persist: Boolean = false, hasData: Boolean = false) =
    ProviderDto(id, name, cloud.name.lowercase(), description, image, ports, state.name.lowercase(), detail, persistable = this.persist != null, persist = persist, hasData = hasData)
