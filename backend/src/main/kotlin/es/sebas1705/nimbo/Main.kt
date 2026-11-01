package es.sebas1705.nimbo

import io.ktor.server.engine.embeddedServer
import io.ktor.server.netty.Netty

fun main() {
    val config = NimboConfig.fromEnv()
    val runtime = CliContainerRuntime(ProcessExec(System.getenv("NIMBO_DOCKER_BIN") ?: "docker"))
    // Bound to loopback on purpose: this app can start and stop containers on the host.
    embeddedServer(Netty, port = config.port, host = "127.0.0.1") {
        nimboModule(config, runtime, StackCatalog.load())
    }.start(wait = true)
}
