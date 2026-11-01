package es.sebas1705.nimbo

import io.ktor.server.application.call
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.route

/** Read-only. Errors from the emulators are mapped to HTTP statuses by StatusPages in [nimboModule]. */
fun Route.inspectRoutes(inspector: Inspector) {
    route("/inspect") {
        route("/aws") {
            get("/s3/buckets") { call.respond(inspector.buckets()) }
            get("/s3/buckets/{bucket}/objects") {
                call.respond(inspector.objects(call.parameters["bucket"].orEmpty(), call.request.queryParameters["prefix"].orEmpty()))
            }
            get("/s3/buckets/{bucket}/object") {
                call.respond(inspector.preview(call.parameters["bucket"].orEmpty(), call.request.queryParameters["key"].orEmpty()))
            }
            get("/sqs/queues") { call.respond(inspector.queues()) }
            get("/sqs/queues/{queue}/messages") { call.respond(inspector.messages(call.parameters["queue"].orEmpty())) }
            get("/dynamodb/tables") { call.respond(inspector.tables()) }
            get("/dynamodb/tables/{table}/items") { call.respond(inspector.items(call.parameters["table"].orEmpty())) }
        }
        get("/gcp/pubsub") {
            call.respond(inspector.pubsub(call.request.queryParameters["project"] ?: "nimbo-local"))
        }
    }
}
