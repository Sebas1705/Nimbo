package es.sebas1705.nimbo

import io.ktor.server.application.Application
import io.ktor.server.engine.embeddedServer
import io.ktor.server.netty.Netty
import io.ktor.server.netty.NettyApplicationEngine
import io.ktor.server.engine.EmbeddedServer
import org.slf4j.LoggerFactory

/**
 * Wires every service to the emulators. Nimbo reaches them at [NimboConfig.emulatorHost], which is
 * `localhost` natively and `host.docker.internal` when Nimbo itself runs in a container.
 */
fun Application.nimboFromConfig(
    config: NimboConfig,
    runtime: ContainerRuntime,
    http: HttpGateway = JavaHttpGateway(),
    shell: Shell = ProcessShell(),
    stacks: StackCatalog = StackCatalog.load(),
    labs: LabCatalog = LabCatalog.load(),
) {
    val h = config.emulatorHost
    nimboModule(
        config, runtime, stacks,
        inspector = Inspector(http, awsEndpoint = "http://$h:4566", gcpEndpoint = "http://$h:4588"),
        chaos = Chaos(http, base = "http://$h:${ChaosTargets.API_PORT}"),
        conformance = Conformance(http, aws = "http://$h:4566", azure = "http://$h:4577", gcp = "http://$h:4588"),
        labs = labs,
        kube = Kube(shell),
        traces = Traces(http, query = "http://$h:16686", otlp = "http://$h:4318"),
    )
}

/** The real server, shared by the command line entry point and the desktop one. */
internal fun buildServer(config: NimboConfig): EmbeddedServer<NettyApplicationEngine, NettyApplicationEngine.Configuration> {
    val docker = System.getenv("NIMBO_DOCKER_BIN") ?: "docker"
    return embeddedServer(Netty, port = config.port, host = config.host) {
        nimboFromConfig(config, CliContainerRuntime(ProcessExec(docker)), shell = ProcessShell(docker))
    }
}

fun main() {
    val config = NimboConfig.fromEnv()
    val log = LoggerFactory.getLogger("nimbo")
    if (config.host !in setOf("127.0.0.1", "localhost")) {
        log.warn("Listening on ${config.host}: publish the port on loopback only (-p 127.0.0.1:${config.port}:${config.port}); Nimbo can control Docker.")
    }
    log.info("Nimbo ${config.version ?: "dev"} on http://${config.host}:${config.port}")
    buildServer(config).start(wait = true)
}
