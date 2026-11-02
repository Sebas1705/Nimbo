package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

/** Update checks, verified downloads, importing an installer from a file, and installing with confirmation. */
fun Route.updateRoutes(updater: Updater) {
    route("/updates") {
        get { call.respond(updater.status()) }
        post("/check") {
            // The page asks on its own with force=false (respects the mode and the daily limit); the button forces it.
            call.respond(updater.check(force = call.request.queryParameters["force"] == "true"))
        }
        post("/download") {
            val file = try { updater.download(call.receive<DownloadRequestDto>().version) } catch (e: UpdateError) {
                return@post call.respond(HttpStatusCode.BadGateway, ErrorDto(e.message ?: "download failed"))
            }
            call.respond(file)
        }
        post("/import") {
            val body = call.receive<ImportUpdateDto>()
            val file = try { updater.import(body.path, body.sha256) } catch (e: UpdateError) {
                return@post call.respond(HttpStatusCode.UnprocessableEntity, ErrorDto(e.message ?: "import failed"))
            }
            call.respond(file)
        }
        post("/install") {
            val body = call.receive<InstallRequestDto>()
            try { updater.install(body.version, body.confirm) } catch (e: UpdateError) {
                return@post call.respond(HttpStatusCode.UnprocessableEntity, ErrorDto(e.message ?: "install failed"))
            }
            call.respond(HttpStatusCode.Accepted, mapOf("message" to "Installing ${body.version}: Nimbo will close and reopen by itself"))
        }
        delete("/files") { call.respond(mapOf("deleted" to updater.clean())) }
    }
}
