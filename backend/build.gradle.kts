plugins {
    kotlin("jvm") version "2.0.21"
    kotlin("plugin.serialization") version "2.0.21"
    id("io.ktor.plugin") version "3.0.1"
}

group = "es.sebas1705"
// The release workflow passes the tag (v0.1.0 -> 0.1.0); local builds use the default below.
version = (findProperty("nimboVersion") as String?) ?: "0.2.0"

repositories {
    mavenCentral()
}

val ktorVersion = "3.0.1"

dependencies {
    implementation("io.ktor:ktor-server-core:$ktorVersion")
    implementation("io.ktor:ktor-server-netty:$ktorVersion")
    implementation("io.ktor:ktor-server-content-negotiation:$ktorVersion")
    implementation("io.ktor:ktor-server-status-pages:$ktorVersion")
    implementation("io.ktor:ktor-serialization-kotlinx-json:$ktorVersion")
    implementation("ch.qos.logback:logback-classic:1.5.12")
    implementation("org.yaml:snakeyaml:2.3")

    testImplementation("io.ktor:ktor-server-test-host:$ktorVersion")
    testImplementation(kotlin("test"))
}

kotlin {
    jvmToolchain(21)
}

application {
    mainClass.set("es.sebas1705.nimbo.MainKt")
}

ktor {
    fatJar {
        archiveFileName.set("nimbo-${project.version}.jar")
    }
}

tasks.withType<Jar>().configureEach {
    manifest {
        attributes["Implementation-Title"] = "Nimbo"
        attributes["Implementation-Version"] = project.version
    }
}

tasks.test {
    useJUnitPlatform()
    // Several tests check files outside this module (guides, labs, stacks, workflows, compose, changelog...).
    // Without declaring them Gradle would call a cached result up to date after one of them changed.
    inputs.files(
        rootProject.file("web/src/guides.ts"),
        rootProject.file("nimbo.yml"),
        rootProject.file("README.md"),
        rootProject.file("CHANGELOG.md"),
        rootProject.file("docker-compose.yml"),
        rootProject.file(".env.example"),
        rootProject.file(".gitignore"),
    ).withPropertyName("repoFiles")
    inputs.dir(rootProject.file("labs")).withPropertyName("labs")
    inputs.dir(rootProject.file(".github/workflows")).withPropertyName("workflows")
    inputs.dir(rootProject.file("packaging")).withPropertyName("packaging")
}

// ---- Packaging: the jar carries the UI, the labs and the default stacks, so it runs from any folder ----

val bundleDir = layout.buildDirectory.dir("bundle")

val bundleWeb by tasks.registering(Copy::class) {
    val dist = rootProject.file("web/dist")
    doFirst {
        if (!dist.resolve("index.html").exists()) {
            throw GradleException("web/dist is missing. Run 'npm ci && npm run build' in web/ first (scripts/package does both).")
        }
    }
    from(dist)
    into(bundleDir.map { it.dir("static") })
}

val bundleDefaults by tasks.registering {
    val labs = rootProject.file("labs")
    val stacks = rootProject.file("nimbo.yml")
    val out = bundleDir.map { it.dir("defaults") }
    inputs.dir(labs)
    inputs.file(stacks)
    outputs.dir(out)
    doLast {
        val target = out.get().asFile
        target.deleteRecursively()
        target.resolve("labs").mkdirs()
        val files = labs.listFiles { f -> f.extension == "yml" }!!.sortedBy { it.name }
        check(files.isNotEmpty()) { "no labs found in $labs" }
        files.forEach { it.copyTo(target.resolve("labs/${it.name}")) }
        // A jar cannot be listed like a folder, so the lab files are indexed here.
        target.resolve("labs/index.txt").writeText(files.joinToString("\n") { it.name } + "\n")
        stacks.copyTo(target.resolve("nimbo.yml"))
    }
}

tasks.named<Jar>("shadowJar") {
    dependsOn(bundleWeb, bundleDefaults)
    from(bundleDir)
}

// ---- Windows desktop app: Nimbo.exe with its own Java, a tray icon and (on a runner with WiX) an .msi ----

tasks.register<JavaExec>("generateIcon") {
    group = "desktop"
    description = "Redraws packaging/nimbo.ico from the code that draws the tray icon"
    classpath = sourceSets.main.get().runtimeClasspath
    mainClass.set("es.sebas1705.nimbo.IconTool")
    args(rootProject.file("packaging/nimbo.ico").path)
}

val desktopInput = layout.buildDirectory.dir("desktop-input")
val desktopOut = layout.buildDirectory.dir("desktop")
val isWindows = System.getProperty("os.name").startsWith("Windows")

val stageDesktop by tasks.registering(Copy::class) {
    dependsOn("shadowJar") // the jar already carries the UI, the labs and the stacks
    from(tasks.named<Jar>("shadowJar").flatMap { it.archiveFile })
    into(desktopInput)
}

// -PdesktopType=app-image (default, a portable folder) | msi | exe  (the last two need WiX on the machine)
val packageDesktop by tasks.registering(Exec::class) {
    group = "desktop"
    description = "Builds Nimbo.exe with jpackage (Windows only)"
    dependsOn(stageDesktop)
    onlyIf { isWindows }
    val type = (findProperty("desktopType") as String?) ?: "app-image"
    val launcher = javaToolchains.launcherFor(java.toolchain).map { it.metadata.installationPath.file("bin/jpackage.exe").asFile }
    inputs.file(rootProject.file("packaging/nimbo.ico"))
    inputs.dir(desktopInput) // the staged jar: without this a rebuilt jar would leave a stale app
    inputs.property("type", type) // app-image and msi must not be mistaken for each other
    outputs.dir(desktopOut)
    doFirst {
        delete(desktopOut)
        executable = launcher.get().path
    }
    val modules = "java.base,java.desktop,java.instrument,java.logging,java.management,java.naming,java.net.http,java.sql,java.xml,jdk.unsupported,jdk.crypto.ec"
    val common = listOf(
        "--type", type,
        "--name", "Nimbo",
        "--app-version", project.version.toString(),
        "--vendor", "Sebas1705",
        "--description", "Your local cloud, no bill",
        "--copyright", "MIT License",
        "--input", desktopInput.get().asFile.path,
        "--main-jar", "nimbo-${project.version}.jar",
        "--main-class", "es.sebas1705.nimbo.DesktopApp",
        "--icon", rootProject.file("packaging/nimbo.ico").path,
        "--dest", desktopOut.get().asFile.path,
        "--add-modules", modules,
        "--java-options", "-Dfile.encoding=UTF-8",
        "--java-options", "-Xmx768m",
    )
    val installer = if (type == "app-image") emptyList() else listOf(
        "--win-menu", "--win-menu-group", "Nimbo", "--win-shortcut", "--win-per-user-install", "--win-dir-chooser",
        // A fixed id is what lets a newer installer replace the older version instead of installing next to it.
        "--win-upgrade-uuid", "7c3f2b9e-4d51-4a8e-9a47-2f1e6c0b5d83",
        "--license-file", rootProject.file("LICENSE").path,
    )
    args(common + installer)
}

tasks.register<Zip>("zipDesktop") {
    group = "desktop"
    description = "Portable Nimbo for Windows: unzip and run Nimbo.exe"
    dependsOn(packageDesktop)
    from(desktopOut.map { it.dir("Nimbo") }) { into("Nimbo") }
    archiveFileName.set("Nimbo-${project.version}-windows-x64.zip")
    destinationDirectory.set(layout.buildDirectory.dir("distributions"))
}
