package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.put
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable

@Serializable data class StatsViewDto(val stats: StatsData, val journal: List<JournalEntry>, val fidelity: List<FidelityRunSummary>)
@Serializable data class DataInfoDto(val location: String?, val files: Map<String, Long>, val distribution: String)
@Serializable data class ImportIdsDto(val ids: List<String>)
@Serializable data class SecretItemDto(val name: String, val set: Boolean, val usedBy: List<String>, val password: Boolean)
@Serializable data class SecretsDto(val available: Boolean, val reason: String?, val items: List<SecretItemDto>)
@Serializable data class SecretValueDto(val value: String)
@Serializable data class PersistDto(val enabled: Boolean)
@Serializable data class RestoredDto(val restored: List<String>)

/** Settings, lab progress, usage statistics, backups and secrets. Everything lives on this machine. */
fun Route.dataRoutes(data: AppData, secrets: SecretStore, config: NimboConfig) {
    get("/settings") { call.respond(data.settings.get()) }
    put("/settings") {
        val before = data.settings.get()
        val after = data.settings.patch(call.receive<SettingsPatch>())
        if (after != before) data.stats.record("settings.change", "settings")
        call.respond(after)
    }

    route("/progress") {
        get { call.respond(data.progress.get()) }
        post("/import") { call.respond(data.progress.importDone(call.receive<ImportIdsDto>().ids)) }
    }

    get("/stats") {
        val limit = call.request.queryParameters["limit"]?.toIntOrNull() ?: 40
        call.respond(StatsViewDto(data.stats.stats(), data.stats.journal(limit), data.fidelity.list().takeLast(30)))
    }

    route("/data") {
        get { call.respond(DataInfoDto(data.store.location?.path, data.store.sizes(), config.distribution)) }
        get("/export") { call.respond(data.export()) }
        post("/import") {
            val restored = data.restore(call.receive<DataBundle>())
            data.stats.record("data.import", "backup", restored.joinToString())
            call.respond(RestoredDto(restored))
        }
        post("/reset") {
            if (call.request.queryParameters["confirm"] != "yes") {
                return@post call.respond(HttpStatusCode.BadRequest, ErrorDto("Add confirm=yes: this deletes data and cannot be undone"))
            }
            when (val what = call.request.queryParameters["what"]) {
                "stats" -> data.stats.reset()
                "progress" -> data.store.delete(DataFiles.PROGRESS)
                "history" -> data.fidelity.clear()
                "all" -> {
                    data.stats.reset()
                    data.fidelity.clear()
                    DataFiles.BACKUP.forEach(data.store::delete)
                }
                else -> return@post call.respond(HttpStatusCode.BadRequest, ErrorDto("what must be stats, progress, history or all (got '${what.orEmpty()}')"))
            }
            call.respond(HttpStatusCode.NoContent)
        }
    }

    route("/secrets") {
        get {
            val set = secrets.names()
            call.respond(SecretsDto(secrets.available, secrets.reason, Secrets.NAMES.map { SecretItemDto(it, it in set, Secrets.usedBy(it), Secrets.isPassword(it)) }))
        }
        route("/{name}") {
            put {
                val name = call.parameters["name"].orEmpty()
                secrets.set(name, call.receive<SecretValueDto>().value)
                data.stats.record("secret.set", name)
                call.respond(HttpStatusCode.NoContent)
            }
            delete {
                val name = call.parameters["name"].orEmpty()
                if (name !in Secrets.NAMES) throw UnknownTarget("Unknown secret '$name'")
                secrets.delete(name)
                data.stats.record("secret.delete", name)
                call.respond(HttpStatusCode.NoContent)
            }
            post("/generate") {
                val name = call.parameters["name"].orEmpty()
                if (name !in Secrets.NAMES) throw UnknownTarget("Unknown secret '$name'")
                if (!Secrets.isPassword(name)) throw InvalidParam("Only passwords are generated: choose the user yourself")
                val value = Secrets.generate()
                secrets.set(name, value)
                data.stats.record("secret.set", name, "generated")
                // The only time the value leaves the store: the person has to be able to connect with it.
                call.respond(SecretValueDto(value))
            }
        }
    }
}
