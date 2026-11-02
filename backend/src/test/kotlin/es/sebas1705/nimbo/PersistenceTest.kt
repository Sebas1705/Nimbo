package es.sebas1705.nimbo

import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PersistenceArgsTest {
    private val env = mapOf("NIMBO_MINIO_USER" to "admin", "NIMBO_MINIO_PASSWORD" to "pw-12345", "NIMBO_POSTGRES_PASSWORD" to "pg")

    @Test
    fun `without persistence nothing is mounted and the label says so`() {
        val args = CliContainerRuntime.buildRunArgs(Catalog.find("postgres")!!, env)
        assertContains(args, "nimbo.persist=false")
        assertFalse("-v" in args)
    }

    @Test
    fun `with persistence a named volume is mounted and the label says so`() {
        val def = Catalog.find("postgres")!!
        val args = CliContainerRuntime.buildRunArgs(def, env, persist = true)
        assertContains(args, "nimbo.persist=true")
        assertContains(args, "nimbo-postgres-data:${def.persist!!.mountPath}")
        assertEquals("-v", args[args.indexOf("nimbo-postgres-data:${def.persist!!.mountPath}") - 1])
    }

    @Test
    fun `redis switches on its append only file`() {
        val args = CliContainerRuntime.buildRunArgs(Catalog.find("redis")!!, env, persist = true)
        assertEquals(listOf("redis-server", "--appendonly", "yes"), args.takeLast(3))
        assertEquals(Catalog.find("redis")!!.command, CliContainerRuntime.buildRunArgs(Catalog.find("redis")!!, env, persist = false).takeLast(Catalog.find("redis")!!.command.size))
    }

    @Test
    fun `floci emulators turn on their storage through environment variables`() {
        val args = CliContainerRuntime.buildRunArgs(Catalog.find("floci-aws")!!, env, persist = true)
        assertContains(args, "FLOCI_STORAGE_MODE=hybrid")
        assertContains(args, "FLOCI_STORAGE_PERSISTENT_PATH=/app/data")
    }

    @Test
    fun `images that run unprivileged are given a writable volume`() {
        // Verified with real containers: a fresh volume is owned by root and these images cannot write to it otherwise.
        listOf("minio", "jaeger").forEach { id ->
            val on = CliContainerRuntime.buildRunArgs(Catalog.find(id)!!, env, persist = true)
            assertEquals("0:0", on[on.indexOf("--user") + 1], id)
            assertFalse("--user" in CliContainerRuntime.buildRunArgs(Catalog.find(id)!!, env, persist = false), id)
        }
    }

    @Test
    fun `a provider without persistence ignores the flag`() {
        val stateless = Catalog.providers.first { it.persist == null }
        val args = CliContainerRuntime.buildRunArgs(stateless, env, persist = true)
        assertContains(args, "nimbo.persist=false")
        assertFalse("-v" in args)
    }

    @Test
    fun `every mount path is absolute, unique per provider and volumes are named after the provider`() {
        Catalog.providers.mapNotNull { d -> d.persist?.let { d to it } }.forEach { (d, spec) ->
            assertTrue(spec.mountPath.startsWith("/"), d.id)
            assertEquals("nimbo-${d.id}-data", CliContainerRuntime.volumeName(d))
        }
        assertTrue(Catalog.providers.count { it.persist != null } >= 8)
    }
}

class PersistenceRuntimeTest {
    private class Docker(var containers: String = "", var volumes: String = "") : Exec {
        val calls = mutableListOf<List<String>>()
        override suspend fun run(args: List<String>, timeoutSeconds: Long): ExecResult {
            calls += args
            return when (args.first()) {
                "ps" -> ExecResult(0, containers)
                "volume" -> if (args[1] == "ls") ExecResult(0, volumes) else ExecResult(0, "")
                else -> ExecResult(0, "")
            }
        }
        fun ran(vararg prefix: String) = calls.any { it.take(prefix.size) == prefix.toList() }
    }

    private val env = mapOf("NIMBO_POSTGRES_PASSWORD" to "pg")
    private val pg = Catalog.find("postgres")!!

    @Test
    fun `containers report how they were created`() = runBlocking {
        val d = Docker("nimbo-postgres|running|Up 1m|nimbo.managed=true,nimbo.persist=true,nimbo.provider=postgres\nnimbo-redis|exited|Exited|nimbo.managed=true,nimbo.persist=false")
        val live = CliContainerRuntime(d).containers()
        assertTrue(live.getValue("postgres").persist)
        assertFalse(live.getValue("redis").persist)
    }

    @Test
    fun `a stopped container made in the other mode is replaced, a matching one is just started`() = runBlocking {
        val matching = Docker("nimbo-postgres|exited|Exited|nimbo.managed=true,nimbo.persist=true")
        CliContainerRuntime(matching).start(pg, env, persist = true)
        assertTrue(matching.ran("start", "nimbo-postgres"))
        assertFalse(matching.ran("rm"))

        val other = Docker("nimbo-postgres|exited|Exited|nimbo.managed=true,nimbo.persist=false")
        CliContainerRuntime(other).start(pg, env, persist = true)
        assertTrue(other.ran("rm", "-f", "nimbo-postgres"))
        assertTrue(other.ran("run"))
        assertFalse(other.ran("volume", "rm"), "switching mode must never touch the volume")
    }

    @Test
    fun `a running container is never replaced`() = runBlocking {
        val d = Docker("nimbo-postgres|running|Up|nimbo.managed=true,nimbo.persist=false")
        CliContainerRuntime(d).start(pg, env, persist = true)
        assertFalse(d.ran("rm"))
    }

    @Test
    fun `saved data is read from the volume names`() = runBlocking {
        val d = Docker(volumes = "nimbo-postgres-data\nnimbo-minio-data\nsomething-else\nnimbo-weird")
        assertEquals(setOf("postgres", "minio"), CliContainerRuntime(d).savedData())
    }

    @Test
    fun `deleting data refuses while running and otherwise removes container and volume`() = runBlocking {
        val running = Docker("nimbo-postgres|running|Up|nimbo.managed=true,nimbo.persist=true")
        val refused = CliContainerRuntime(running).deleteData(pg)
        assertTrue(refused.isFailure)
        assertFalse(running.ran("volume", "rm"))

        val stopped = Docker("nimbo-postgres|exited|Exited|nimbo.managed=true,nimbo.persist=true")
        assertTrue(CliContainerRuntime(stopped).deleteData(pg).isSuccess)
        assertTrue(stopped.ran("rm", "-f", "nimbo-postgres"))
        assertTrue(stopped.ran("volume", "rm", "nimbo-postgres-data"))
    }

    @Test
    fun `deleting data that does not exist is not an error`() = runBlocking {
        val exec = Exec { args, _ -> if (args.first() == "volume") ExecResult(1, "Error: No such volume: nimbo-postgres-data") else ExecResult(0, "") }
        assertTrue(CliContainerRuntime(exec).deleteData(pg).isSuccess)
    }

    @Test
    fun `docker unavailable means no saved data and no containers`() = runBlocking {
        val down = Exec { _, _ -> ExecResult(1, "daemon not running") }
        assertTrue(CliContainerRuntime(down).savedData().isEmpty())
        assertNull(CliContainerRuntime(down).containers()["postgres"])
    }
}
