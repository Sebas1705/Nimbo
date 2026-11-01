package es.sebas1705.nimbo

import io.ktor.server.application.call
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

/** Failures of `kubectl` itself (bad manifest, unknown resource) are mapped to 422 by StatusPages in [nimboModule]. */
fun Route.kubeRoutes(kube: Kube) {
    route("/k8s") {
        get { call.respond(kube.overview()) }
        get("/samples") { call.respond(Kube.samples) }
        get("/kubeconfig") { call.respond(kube.kubeconfig()) }
        post("/apply") { call.respond(kube.apply(call.receive<ApplyManifestDto>().manifest)) }
        post("/namespaces/{ns}/deployments/{name}/scale") {
            call.respond(kube.scale(call.parameters["ns"].orEmpty(), call.parameters["name"].orEmpty(), call.receive<ScaleDto>().replicas))
        }
        get("/namespaces/{ns}/pods/{name}/logs") {
            val tail = call.request.queryParameters["tail"]?.toIntOrNull() ?: 200
            call.respond(kube.logs(call.parameters["ns"].orEmpty(), call.parameters["name"].orEmpty(), tail))
        }
        delete("/namespaces/{ns}/{kind}/{name}") {
            call.respond(kube.delete(call.parameters["kind"].orEmpty(), call.parameters["ns"].orEmpty(), call.parameters["name"].orEmpty()))
        }
    }
}
