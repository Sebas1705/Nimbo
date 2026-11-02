package es.sebas1705.nimbo

import io.ktor.server.application.Application
import io.ktor.server.engine.embeddedServer
import io.ktor.server.netty.Netty
import io.ktor.server.netty.NettyApplicationEngine
import io.ktor.server.engine.EmbeddedServer
import org.slf4j.LoggerFactory
import java.io.File

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
    data: AppData = AppData.onDisk(dataDir(System.getenv(), System.getProperty("user.home"))),
    secrets: SecretStore = defaultSecretStore(data),
    updater: Updater = defaultUpdater(config, data),
    images: Images = defaultImages(config, data),
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
        data = data,
        secrets = secrets,
        updater = updater,
        images = images,
    )
}

internal fun defaultImages(config: NimboConfig, data: AppData): Images {
    // For the Windows app the launcher sits in the install folder; an `images` folder beside it is where a kit is unpacked.
    val appDir = if (config.distribution == "desktop") ProcessHandle.current().info().command().orElse(null)?.let { File(it).parentFile } else null
    return Images(ProcessExec(System.getenv("NIMBO_DOCKER_BIN") ?: "docker"), { Images.searchFolders(System.getenv(), data, appDir) }, data)
}

internal fun defaultUpdater(config: NimboConfig, data: AppData): Updater {
    val folder = data.store.location ?: File(System.getProperty("java.io.tmpdir"), "nimbo-data")
    return Updater(
        config, data, JavaDownloader(), folder,
        installer = if (config.distribution == "desktop") WindowsInstaller(File(folder, "updates")) else Installer { _, _, _ -> throw UpdateError("This installation cannot update itself") },
        // The installer waits for this process to end, so leave a moment for the answer to reach the page first.
        quit = { Thread { Thread.sleep(1500); kotlin.system.exitProcess(0) }.apply { isDaemon = true }.start() },
    )
}

/** Passwords are protected by Windows (DPAPI) where that exists; elsewhere Nimbo says so instead of storing them in clear. */
internal fun defaultSecretStore(data: AppData): SecretStore {
    val windows = System.getProperty("os.name").orEmpty().startsWith("Windows")
    return if (windows && data.store.location != null) DpapiSecretStore(data.store, ProcessShell("powershell.exe"))
    else UnavailableSecretStore("Secrets can only be protected on Windows; elsewhere use environment variables or a .env file.")
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
