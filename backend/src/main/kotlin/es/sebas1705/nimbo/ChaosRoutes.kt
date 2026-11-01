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

fun Route.chaosRoutes(chaos: Chaos) {
    route("/chaos") {
        get { call.respond(chaos.view()) }
        post("/reset") { call.respond(chaos.resetAll()) }
        route("/targets/{id}") {
            post("/{preset}") {
                val body = runCatching { call.receiveNullable<ApplyDto>() }.getOrNull() ?: ApplyDto()
                call.respond(chaos.apply(call.parameters["id"].orEmpty(), call.parameters["preset"].orEmpty(), body.params))
            }
            delete("/toxics") { call.respond(chaos.clear(call.parameters["id"].orEmpty())) }
            delete("/toxics/{name}") { call.respond(chaos.removeToxic(call.parameters["id"].orEmpty(), call.parameters["name"].orEmpty())) }
        }
    }
}
