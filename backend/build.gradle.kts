plugins {
    kotlin("jvm") version "2.0.21"
    kotlin("plugin.serialization") version "2.0.21"
    id("io.ktor.plugin") version "3.0.1"
}

group = "es.sebas1705"
// The release workflow passes the tag (v0.1.0 -> 0.1.0); local builds use the default below.
version = (findProperty("nimboVersion") as String?) ?: "0.1.0"

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
