package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.traceRoutes(traces: Traces) {
    route("/traces") {
        get("/services") { call.respond(traces.services()) }
        get {
            val service = call.request.queryParameters["service"]
                ?: return@get call.respond(HttpStatusCode.BadRequest, ErrorDto("service is required"))
            call.respond(traces.list(service, call.request.queryParameters["limit"]?.toIntOrNull() ?: 20))
        }
        post("/sample") { call.respond(traces.sample(call.request.queryParameters["error"] == "true")) }
        get("/{id}") { call.respond(traces.detail(call.parameters["id"].orEmpty())) }
    }
}
