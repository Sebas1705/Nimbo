package es.sebas1705.nimbo

import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.server.testing.testApplication
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

internal const val CLUSTER = """{"apiVersion":"v1","kind":"List","items":[
{"kind":"Node","metadata":{"name":"n1"},"status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.34.1+k3s1"},"capacity":{"cpu":"32","memory":"32421584Ki"}}},
{"kind":"Namespace","metadata":{"name":"kube-system"}},
{"kind":"Namespace","metadata":{"name":"default"}},
{"kind":"Deployment","metadata":{"name":"hello","namespace":"default"},"spec":{"replicas":3},"status":{"readyReplicas":2}},
{"kind":"Deployment","metadata":{"name":"idle","namespace":"default"},"spec":{"replicas":0},"status":{}},
{"kind":"Pod","metadata":{"name":"hello-1","namespace":"default"},"spec":{"nodeName":"n1","containers":[{"name":"web"}]},"status":{"phase":"Running","startTime":"2026-11-01T18:43:17Z","containerStatuses":[{"ready":true,"restartCount":0,"state":{"running":{}}}]}},
{"kind":"Pod","metadata":{"name":"crashy-1","namespace":"default"},"spec":{"nodeName":"n1","containers":[{"name":"boom"}]},"status":{"phase":"Running","containerStatuses":[{"ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},
{"kind":"Pod","metadata":{"name":"going","namespace":"default","deletionTimestamp":"2026-11-01T19:00:00Z"},"spec":{"containers":[{"name":"a"},{"name":"b"}]},"status":{"phase":"Running"}},
{"kind":"Pod","metadata":{"name":"flap-1","namespace":"default"},"spec":{"containers":[{"name":"x"}]},"status":{"phase":"Running","containerStatuses":[{"ready":false,"restartCount":2,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}},
{"kind":"Pod","metadata":{"name":"job-done","namespace":"default"},"spec":{"containers":[{"name":"x"}]},"status":{"phase":"Succeeded","containerStatuses":[{"ready":false,"restartCount":0,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}},
{"kind":"Pod","metadata":{"name":"healed-1","namespace":"default"},"spec":{"containers":[{"name":"x"}]},"status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":3,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error"}}}]}},
{"kind":"Service","metadata":{"name":"hello","namespace":"default"},"spec":{"type":"NodePort","clusterIP":"10.43.149.167","ports":[{"port":80,"nodePort":30080,"protocol":"TCP"}]}},
{"kind":"Service","metadata":{"name":"kubernetes","namespace":"default"},"spec":{"clusterIP":"10.43.0.1","ports":[{"port":443,"protocol":"TCP"}]}}
]}"""

/** Records every docker invocation; [respond] decides the result. */
internal class ScriptedShell(var respond: (List<String>, String?) -> ExecResult) : Shell {
    val calls = mutableListOf<List<String>>()
    val stdins = mutableListOf<String?>()
    override suspend fun run(args: List<String>, stdin: String?, timeoutSeconds: Long): ExecResult {
        calls += args
        stdins += stdin
        return respond(args, stdin)
    }
}

internal fun healthy(extra: (List<String>, String?) -> ExecResult? = { _, _ -> null }) = ScriptedShell { args, stdin ->
    extra(args, stdin) ?: when {
        "--raw=/readyz" in args -> ExecResult(0, "ok")
        "get" in args && "nodes,namespaces,deployments,pods,services" in args -> ExecResult(0, CLUSTER)
        "cat" in args -> ExecResult(0, "apiVersion: v1\nclusters: []")
        else -> ExecResult(0, "done")
    }
}

class KubeServiceTest {
    @Test
    fun `overview parses the cluster`() = runBlocking {
        val o = Kube(healthy()).overview()
        assertTrue(o.available)
        assertEquals("v1.34.1+k3s1", o.version)
        assertEquals(listOf("default", "kube-system"), o.namespaces)
        val node = o.nodes.single()
        assertTrue(node.ready)
        assertEquals("32", node.cpu)

        assertEquals(listOf("hello" to (2 to 3), "idle" to (0 to 0)), o.deployments.map { it.name to (it.ready to it.desired) })

        val pods = o.pods.associateBy { it.name }
        assertEquals("Running", pods.getValue("hello-1").status)
        assertEquals("1/1", pods.getValue("hello-1").ready)
        assertEquals("CrashLoopBackOff", pods.getValue("crashy-1").status, "a waiting reason is more useful than the phase")
        assertEquals(4, pods.getValue("crashy-1").restarts)
        assertEquals("0/1", pods.getValue("crashy-1").ready)
        assertEquals("Terminating", pods.getValue("going").status)
        assertEquals("Error", pods.getValue("flap-1").status, "a restarting container that is momentarily running is an Error, not Running")
        assertEquals("Completed", pods.getValue("job-done").status)
        assertEquals("Running", pods.getValue("healed-1").status, "restarted in the past but ready now")
        assertEquals("0/2", pods.getValue("going").ready, "no container statuses yet: use the spec")

        val hello = o.services.first { it.name == "hello" }
        assertEquals("NodePort", hello.type)
        assertEquals(listOf("80:30080/TCP"), hello.ports)
        assertEquals(listOf(30080), hello.nodePorts)
        assertEquals("ClusterIP", o.services.first { it.name == "kubernetes" }.type, "type defaults to ClusterIP")
    }

    @Test
    fun `overview uses docker exec with kubectl inside the container, as an argument list`() = runBlocking {
        val shell = healthy()
        Kube(shell).overview()
        assertEquals(listOf("exec", "nimbo-k3s", "kubectl", "get", "--raw=/readyz"), shell.calls.first())
        assertEquals(listOf("exec", "nimbo-k3s", "kubectl", "get", "nodes,namespaces,deployments,pods,services", "-A", "-o", "json"), shell.calls[1])
    }

    @Test
    fun `a missing container and a starting cluster are told apart, and neither throws`() = runBlocking {
        val missing = Kube(ScriptedShell { _, _ -> ExecResult(1, "Error response from daemon: No such container: nimbo-k3s") }).overview()
        assertFalse(missing.available)
        assertContains(missing.reason!!, "not running")
        val stopped = Kube(ScriptedShell { _, _ -> ExecResult(1, "container abc is not running") }).overview()
        assertContains(stopped.reason!!, "not running")
        val starting = Kube(ScriptedShell { _, _ -> ExecResult(1, "the server is currently unable to handle the request") }).overview()
        assertFalse(starting.available)
        assertContains(starting.reason!!, "starting")
        assertTrue(starting.pods.isEmpty())
    }

    @Test
    fun `a failing listing is an error not an empty cluster`() {
        val shell = ScriptedShell { args, _ -> if ("--raw=/readyz" in args) ExecResult(0, "ok") else ExecResult(1, "forbidden") }
        assertFailsWith<KubeError> { runBlocking { Kube(shell).overview() } }
    }

    @Test
    fun `apply pipes the manifest through stdin`() = runBlocking {
        val shell = healthy { args, _ -> if ("apply" in args) ExecResult(0, "deployment.apps/hello created") else null }
        val out = Kube(shell).apply("kind: Deployment\n")
        assertEquals("deployment.apps/hello created", out.output)
        assertEquals(listOf("exec", "-i", "nimbo-k3s", "kubectl", "apply", "-f", "-"), shell.calls.single())
        assertEquals("kind: Deployment\n", shell.stdins.single())
    }

    @Test
    fun `apply rejects empty and oversized manifests and reports kubectl errors`() {
        val shell = healthy { args, _ -> if ("apply" in args) ExecResult(1, "error: unable to recognize") else null }
        val kube = Kube(shell)
        assertFailsWith<InvalidName> { runBlocking { kube.apply("   ") } }
        assertFailsWith<InvalidName> { runBlocking { kube.apply("a".repeat(Kube.MAX_MANIFEST + 1)) } }
        assertTrue(shell.calls.isEmpty(), "nothing is sent for invalid input")
        val ex = assertFailsWith<KubeError> { runBlocking { kube.apply("nonsense: true") } }
        assertContains(ex.message!!, "unable to recognize")
    }

    @Test
    fun `scale validates names and bounds before calling kubectl`() = runBlocking {
        val shell = healthy()
        val kube = Kube(shell)
        kube.scale("default", "hello", 5)
        assertEquals(listOf("exec", "nimbo-k3s", "kubectl", "scale", "deployment/hello", "-n", "default", "--replicas=5"), shell.calls.single())
        shell.calls.clear()
        listOf(-1, Kube.MAX_REPLICAS + 1).forEach { n -> assertFailsWith<InvalidName> { kube.scale("default", "hello", n) } }
        listOf("Bad_Name", "a b", "-x", "../x", "").forEach { bad ->
            assertFailsWith<InvalidName>(bad) { kube.scale("default", bad, 1) }
            assertFailsWith<InvalidName>(bad) { kube.scale(bad, "hello", 1) }
        }
        assertTrue(shell.calls.isEmpty())
    }

    @Test
    fun `delete only touches pods, deployments and services outside system namespaces`() = runBlocking {
        val shell = healthy()
        val kube = Kube(shell)
        kube.delete("pod", "default", "hello-1")
        assertEquals(listOf("exec", "nimbo-k3s", "kubectl", "delete", "pod", "hello-1", "-n", "default"), shell.calls.single())
        shell.calls.clear()
        assertFailsWith<InvalidName> { kube.delete("namespace", "default", "default") }
        assertFailsWith<InvalidName> { kube.delete("secret", "default", "x") }
        assertFailsWith<InvalidName> { kube.delete("pod,secret", "default", "x") }
        listOf("kube-system", "kube-public", "kube-node-lease").forEach { ns -> assertFailsWith<InvalidName>(ns) { kube.delete("deployment", ns, "coredns") } }
        assertFailsWith<InvalidName> { kube.delete("pod", "default", "--all") }
        assertTrue(shell.calls.isEmpty())
    }

    @Test
    fun `logs clamp the tail and validate names`() = runBlocking {
        val shell = healthy { args, _ -> if ("logs" in args) ExecResult(0, "line") else null }
        val kube = Kube(shell)
        assertEquals("line", kube.logs("default", "p", tail = 999_999).output)
        assertContains(shell.calls.single(), "--tail=2000")
        shell.calls.clear()
        kube.logs("default", "p", tail = 0)
        assertContains(shell.calls.single(), "--tail=1")
        assertFailsWith<InvalidName> { kube.logs("default", "p;ls", 5) }
        assertFailsWith<KubeError> { Kube(ScriptedShell { _, _ -> ExecResult(1, "pod not found") }).logs("default", "p") }
    }

    @Test
    fun `kubeconfig is read from the container`() = runBlocking {
        val shell = healthy()
        val cfg = Kube(shell).kubeconfig()
        assertEquals("nimbo-k3s.kubeconfig.yaml", cfg.filename)
        assertContains(cfg.content, "apiVersion")
        assertEquals(listOf("exec", "nimbo-k3s", "cat", "/etc/rancher/k3s/k3s.yaml"), shell.calls.single())
        assertFailsWith<KubeError> { Kube(ScriptedShell { _, _ -> ExecResult(1, "no such container") }).kubeconfig() }
    }

    @Test
    fun `samples are well formed and the nodeport they use is published by the k3s provider`() {
        assertEquals(Kube.samples.size, Kube.samples.map { it.id }.toSet().size)
        Kube.samples.forEach { s ->
            assertContains(s.manifest, "apiVersion:")
            assertContains(s.manifest, "kind: Deployment")
            assertTrue(s.manifest.length < Kube.MAX_MANIFEST)
        }
        assertContains(Kube.samples.first { it.id == "hello" }.manifest, "nodePort: 30080")
        assertTrue(30080 in Catalog.find("k3s")!!.ports)
    }
}

class K3sProviderTest {
    @Test
    fun `k3s runs privileged with tmpfs mounts, loopback ports and no secrets`() {
        val def = Catalog.find("k3s")!!
        val args = CliContainerRuntime.buildRunArgs(def, emptyMap())
        assertContains(args, "--privileged")
        assertEquals(listOf("--tmpfs", "/run", "--tmpfs", "/var/run"), args.subList(args.indexOf("--tmpfs"), args.indexOf("--tmpfs") + 4))
        assertContains(args, "127.0.0.1:6443:6443")
        assertContains(args, "K3S_KUBECONFIG_MODE=644")
        assertEquals(listOf("server", "--disable=traefik"), args.takeLast(2))
        assertTrue(args.indexOf("--privileged") < args.indexOf("rancher/k3s"), "flags must come before the image")
        assertEquals("nimbo-k3s", CliContainerRuntime.containerName(def), "Kube talks to this exact container name")
    }

    @Test
    fun `jaeger publishes ui and both otlp ports`() {
        assertEquals(listOf(16686, 4317, 4318), Catalog.find("jaeger")!!.ports)
    }
}

class KubeApiTest {
    private fun app(shell: Shell = healthy(), token: String? = null, block: suspend io.ktor.server.testing.ApplicationTestBuilder.() -> Unit) = testApplication {
        application { nimboModule(NimboConfig(apiToken = token), FakeRuntime(), kube = Kube(shell)) }
        block()
    }

    @Test
    fun `overview, samples and kubeconfig`() = app {
        val overview = Json.parseToJsonElement(client.get("/api/k8s").bodyAsText()).jsonObject
        assertEquals("true", overview["available"]!!.jsonPrimitive.content)
        assertContains(client.get("/api/k8s/samples").bodyAsText(), "\"id\":\"hello\"")
        assertContains(client.get("/api/k8s/kubeconfig").bodyAsText(), "apiVersion")
    }

    @Test
    fun `a stopped cluster is a normal answer, not an error`() = app(ScriptedShell { _, _ -> ExecResult(1, "No such container") }) {
        val response = client.get("/api/k8s")
        assertEquals(HttpStatusCode.OK, response.status)
        assertContains(response.bodyAsText(), "\"available\":false")
    }

    @Test
    fun `apply returns the output and maps kubectl failures to 422`() {
        val shell = healthy { args, _ -> if ("apply" in args) ExecResult(0, "created") else null }
        app(shell) {
            val ok = client.post("/api/k8s/apply") { contentType(ContentType.Application.Json); setBody("""{"manifest":"kind: Pod"}""") }
            assertEquals(HttpStatusCode.OK, ok.status)
            assertContains(ok.bodyAsText(), "created")
        }
        app(healthy { args, _ -> if ("apply" in args) ExecResult(1, "error: bad yaml") else null }) {
            val bad = client.post("/api/k8s/apply") { contentType(ContentType.Application.Json); setBody("""{"manifest":"x"}""") }
            assertEquals(HttpStatusCode.UnprocessableEntity, bad.status)
            assertContains(bad.bodyAsText(), "bad yaml")
            val empty = client.post("/api/k8s/apply") { contentType(ContentType.Application.Json); setBody("""{"manifest":""}""") }
            assertEquals(HttpStatusCode.BadRequest, empty.status)
        }
    }

    @Test
    fun `scale, delete and logs go through validation`() {
        val shell = healthy()
        app(shell) {
            assertEquals(HttpStatusCode.OK, client.post("/api/k8s/namespaces/default/deployments/hello/scale") { contentType(ContentType.Application.Json); setBody("""{"replicas":3}""") }.status)
            assertEquals(HttpStatusCode.BadRequest, client.post("/api/k8s/namespaces/default/deployments/hello/scale") { contentType(ContentType.Application.Json); setBody("""{"replicas":999}""") }.status)
            assertEquals(HttpStatusCode.OK, client.delete("/api/k8s/namespaces/default/pod/hello-1").status)
            assertEquals(HttpStatusCode.BadRequest, client.delete("/api/k8s/namespaces/kube-system/deployment/coredns").status)
            assertEquals(HttpStatusCode.BadRequest, client.delete("/api/k8s/namespaces/default/secret/x").status)
            assertEquals(HttpStatusCode.OK, client.get("/api/k8s/namespaces/default/pods/hello-1/logs?tail=5").status)
            assertEquals(HttpStatusCode.BadRequest, client.get("/api/k8s/namespaces/default/pods/BAD/logs").status)
            assertTrue(shell.calls.none { "secret" in it || "kube-system" in it || "999" in it.joinToString() }, "rejected requests never reach kubectl")
        }
    }

    @Test
    fun `everything under k8s needs the token, including the kubeconfig`() = app(token = "t") {
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/k8s").status)
        assertEquals(HttpStatusCode.Unauthorized, client.get("/api/k8s/kubeconfig").status)
        assertEquals(HttpStatusCode.Unauthorized, client.post("/api/k8s/apply") { contentType(ContentType.Application.Json); setBody("""{"manifest":"x"}""") }.status)
        assertEquals(HttpStatusCode.Unauthorized, client.delete("/api/k8s/namespaces/default/pod/x").status)
    }
}
