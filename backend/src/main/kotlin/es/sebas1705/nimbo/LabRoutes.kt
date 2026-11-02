package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.labRoutes(labs: LabCatalog, verifier: LabVerifier, data: AppData = AppData()) {
    route("/labs") {
        get { call.respond(LabListDto(labs.labs.map { it.dto }, labs.problems)) }
        post("/{id}/verify") {
            val id = call.parameters["id"].orEmpty()
            val lab = labs.find(id) ?: return@post call.respond(HttpStatusCode.NotFound, ErrorDto("Unknown lab '$id'"))
            val firstTime = id !in data.progress.completed()
            val result = verifier.verify(lab)
            data.progress.attempt(id, result.passed)
            data.stats.record("lab.verify", id, if (result.passed) "passed" else "not yet")
            if (result.passed && firstTime) data.stats.record("lab.complete", id)
            call.respond(result)
        }
    }
}
