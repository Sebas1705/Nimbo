package es.sebas1705.nimbo

import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.coroutines.sync.Mutex

/** Keeps the latest result of every suite so a partial run (one cloud) does not discard the others. */
class FidelityStore {
    private val results = linkedMapOf<String, SuiteResultDto>()
    @Volatile var updatedAt: Long? = null
    val lock = Mutex()

    @Synchronized fun merge(report: FidelityReportDto) {
        report.suites.forEach { results[it.id] = it }
        updatedAt = report.startedAt + report.durationMs
    }

    @Synchronized fun snapshot(order: List<SuiteRefDto>): List<SuiteResultDto> = order.mapNotNull { results[it.id] }
}

fun Route.fidelityRoutes(conformance: Conformance, store: FidelityStore) {
    fun state(): FidelityStateDto {
        val results = store.snapshot(conformance.catalog)
        return FidelityStateDto(store.lock.isLocked, store.updatedAt, conformance.catalog, results, results.groupingBy { it.status }.eachCount())
    }

    route("/fidelity") {
        get { call.respond(state()) }
        post("/run") {
            val cloud = call.request.queryParameters["cloud"]?.takeIf { it.isNotEmpty() }
            val suite = call.request.queryParameters["suite"]?.takeIf { it.isNotEmpty() }
            if (cloud != null && cloud !in setOf("aws", "azure", "gcp")) {
                return@post call.respond(HttpStatusCode.BadRequest, ErrorDto("cloud must be aws, azure or gcp"))
            }
            if (suite != null && conformance.catalog.none { it.id == suite && (cloud == null || it.cloud == cloud) }) {
                return@post call.respond(HttpStatusCode.BadRequest, ErrorDto("Unknown suite '$suite'"))
            }
            if (!store.lock.tryLock()) return@post call.respond(HttpStatusCode.Conflict, ErrorDto("A run is already in progress"))
            try {
                store.merge(conformance.run(cloud, suite))
            } finally {
                store.lock.unlock()
            }
            call.respond(state())
        }
    }
}
