package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.request.receiveNullable
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.chaosRoutes(chaos: Chaos, stats: StatsService? = null) {
    route("/chaos") {
        get { call.respond(chaos.view()) }
        post("/reset") {
            val view = chaos.resetAll()
            stats?.record("chaos.reset", "all")
            call.respond(view)
        }
        route("/targets/{id}") {
            post("/{preset}") {
                val body = runCatching { call.receiveNullable<ApplyDto>() }.getOrNull() ?: ApplyDto()
                val id = call.parameters["id"].orEmpty()
                val preset = call.parameters["preset"].orEmpty()
                val result = chaos.apply(id, preset, body.params)
                stats?.record("chaos.apply", id, preset)
                call.respond(result)
            }
            delete("/toxics") {
                val id = call.parameters["id"].orEmpty()
                val result = chaos.clear(id)
                stats?.record("chaos.clear", id)
                call.respond(result)
            }
            delete("/toxics/{name}") { call.respond(chaos.removeToxic(call.parameters["id"].orEmpty(), call.parameters["name"].orEmpty())) }
        }
    }
}
