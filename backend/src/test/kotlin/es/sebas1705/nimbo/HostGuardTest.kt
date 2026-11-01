package es.sebas1705.nimbo

import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.server.testing.testApplication
import kotlin.test.Test
import kotlin.test.assertEquals

class HostGuardTest {
    @Test
    fun `loopback hosts are served`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime()) }
        listOf("localhost", "localhost:8080", "127.0.0.1:8766", "LOCALHOST").forEach { host ->
            assertEquals(HttpStatusCode.OK, client.get("/api/health") { header(HttpHeaders.Host, host) }.status, host)
        }
    }

    @Test
    fun `any other host is refused for the api and the ui alike`() = testApplication {
        application { nimboModule(NimboConfig(), FakeRuntime()) }
        listOf("evil.example", "evil.example:8080", "localhost.evil.example", "127.0.0.1.evil.example", "10.0.0.5:8080", "0.0.0.0:8080").forEach { host ->
            assertEquals(HttpStatusCode.Forbidden, client.get("/api/health") { header(HttpHeaders.Host, host) }.status, host)
            assertEquals(HttpStatusCode.Forbidden, client.get("/api/providers") { header(HttpHeaders.Host, host) }.status, host)
            assertEquals(HttpStatusCode.Forbidden, client.get("/") { header(HttpHeaders.Host, host) }.status, host)
        }
    }

    @Test
    fun `extra hosts can be allowed explicitly`() = testApplication {
        application { nimboModule(NimboConfig(allowedHosts = setOf("nimbo.lan")), FakeRuntime()) }
        assertEquals(HttpStatusCode.OK, client.get("/api/health") { header(HttpHeaders.Host, "Nimbo.lan:443") }.status)
        assertEquals(HttpStatusCode.Forbidden, client.get("/api/health") { header(HttpHeaders.Host, "other.lan") }.status)
    }

    @Test
    fun `allowed hosts come from the environment`() {
        val config = NimboConfig.fromEnv(mapOf("NIMBO_ALLOWED_HOSTS" to " Nimbo.lan , ,box.local"))
        assertEquals(setOf("nimbo.lan", "box.local"), config.allowedHosts)
        assertEquals(emptySet(), NimboConfig.fromEnv(emptyMap()).allowedHosts)
    }
}
