package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

/** Which emulator images are on this machine, and how to bring the missing ones in: pull them or load a pack. */
fun Route.imageRoutes(images: Images) {
    route("/images") {
        get { call.respond(images.status()) }
        post("/load") {
            val loaded = try { images.load(call.receive<LoadImagesDto>().path) } catch (e: UpdateError) {
                return@post call.respond(HttpStatusCode.UnprocessableEntity, ErrorDto(e.message ?: "load failed"))
            }
            call.respond(loaded)
        }
        post("/pull") {
            try { images.pull(call.receive<PullImageDto>().provider) } catch (e: UpdateError) {
                return@post call.respond(HttpStatusCode.BadGateway, ErrorDto(e.message ?: "pull failed"))
            }
            call.respond(HttpStatusCode.NoContent)
        }
    }
}
