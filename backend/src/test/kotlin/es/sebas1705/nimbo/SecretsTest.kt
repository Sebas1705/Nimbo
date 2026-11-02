package es.sebas1705.nimbo

import kotlinx.coroutines.runBlocking
import java.nio.file.Files
import java.util.Base64
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

internal class MemorySecrets(override val available: Boolean = true) : SecretStore {
    override val reason: String? = if (available) null else "not here"
    val values = linkedMapOf<String, String>()
    override suspend fun names() = values.keys.toSet()
    override suspend fun get(name: String) = values[name]
    override suspend fun set(name: String, value: String) { Secrets.validate(name, value); values[name] = value }
    override suspend fun delete(name: String) { values.remove(name) }
}

class SecretsTest {
    @Test
    fun `only the passwords and users providers read are known`() {
        assertEquals(setOf("NIMBO_MINIO_USER", "NIMBO_MINIO_PASSWORD", "NIMBO_POSTGRES_PASSWORD"), Secrets.NAMES.toSet())
        assertEquals(listOf("minio"), Secrets.usedBy("NIMBO_MINIO_USER"))
        assertTrue(Secrets.isPassword("NIMBO_POSTGRES_PASSWORD"))
        assertFalse(Secrets.isPassword("NIMBO_MINIO_USER"))
    }

    @Test
    fun `validation explains what is wrong`() {
        assertFailsWith<UnknownTarget> { Secrets.validate("HOME", "x") }
        assertFailsWith<InvalidParam> { Secrets.validate("NIMBO_POSTGRES_PASSWORD", "") }
        assertFailsWith<InvalidParam> { Secrets.validate("NIMBO_POSTGRES_PASSWORD", "x".repeat(129)) }
        assertFailsWith<InvalidParam> { Secrets.validate("NIMBO_POSTGRES_PASSWORD", "line\nbreak") }
        assertFailsWith<InvalidParam> { Secrets.validate("NIMBO_MINIO_PASSWORD", "short") }
        assertFailsWith<InvalidParam> { Secrets.validate("NIMBO_MINIO_USER", "ab") }
        Secrets.validate("NIMBO_MINIO_PASSWORD", "long-enough")
        Secrets.validate("NIMBO_POSTGRES_PASSWORD", "x")
    }

    @Test
    fun `generated passwords are long, varied and free of look-alikes`() {
        val a = Secrets.generate()
        val b = Secrets.generate()
        assertEquals(24, a.length)
        assertNotEquals(a, b)
        assertTrue(a.none { it in "0OIl1" })
        Secrets.validate("NIMBO_MINIO_PASSWORD", a)
    }

    @Test
    fun `the environment wins over the store and only what is missing is read`() = runBlocking {
        val store = MemorySecrets()
        store.set("NIMBO_POSTGRES_PASSWORD", "from-store")
        store.set("NIMBO_MINIO_PASSWORD", "minio-from-store")
        val pg = Catalog.find("postgres")!!

        assertEquals("from-env", Secrets.envFor(pg, mapOf("NIMBO_POSTGRES_PASSWORD" to "from-env"), store)["NIMBO_POSTGRES_PASSWORD"])
        val merged = Secrets.envFor(pg, mapOf("OTHER" to "1"), store)
        assertEquals("from-store", merged["NIMBO_POSTGRES_PASSWORD"])
        assertFalse("NIMBO_MINIO_PASSWORD" in merged, "a provider only gets the secrets it reads")
        assertEquals("1", merged["OTHER"])
    }

    @Test
    fun `an unavailable store changes nothing`() = runBlocking {
        val env = mapOf("A" to "b")
        assertEquals(env, Secrets.envFor(Catalog.find("postgres")!!, env, MemorySecrets(available = false)))
        val none = UnavailableSecretStore("because")
        assertFalse(none.available)
        assertTrue(none.names().isEmpty())
        assertNull(none.get("NIMBO_MINIO_USER"))
        assertFailsWith<InvalidParam> { none.set("NIMBO_MINIO_USER", "abcd") }
    }
}

/** The Windows store, with PowerShell replaced by a fake that records what it was asked and never sees clear text in arguments. */
class DpapiStoreTest {
    private class FakePowerShell : Shell {
        val calls = mutableListOf<Pair<List<String>, String?>>()
        override suspend fun run(args: List<String>, stdin: String?, timeoutSeconds: Long): ExecResult {
            calls += args to stdin
            // Reversing the bytes is its own inverse, so Protect followed by Unprotect gives the value back.
            val out = Base64.getDecoder().decode(stdin!!.trim()).reversedArray()
            return ExecResult(0, "noise\n" + Base64.getEncoder().encodeToString(out))
        }
    }

    @Test
    fun `values round trip, are stored encrypted and never travel in arguments`() = runBlocking {
        val shell = FakePowerShell()
        val data = MemoryDataStore()
        val store = DpapiSecretStore(data, shell)

        store.set("NIMBO_POSTGRES_PASSWORD", "s3cret-value")
        assertEquals("s3cret-value", store.get("NIMBO_POSTGRES_PASSWORD"))
        assertEquals(setOf("NIMBO_POSTGRES_PASSWORD"), store.names())

        val onDisk = data.read(DataFiles.SECRETS)!!
        assertFalse("s3cret-value" in onDisk)
        assertFalse(Base64.getEncoder().encodeToString("s3cret-value".toByteArray()) in onDisk, "stored text must be the protected blob, not the plain value")
        shell.calls.forEach { (args, stdin) ->
            assertTrue(args.none { "s3cret-value" in it }, "secret leaked into the command line")
            assertTrue(stdin != null)
        }
        assertContains(shell.calls.first().first.last(), "CurrentUser")
    }

    @Test
    fun `delete and unknown values`() = runBlocking {
        val store = DpapiSecretStore(MemoryDataStore(), FakePowerShell())
        assertNull(store.get("NIMBO_MINIO_USER"))
        store.set("NIMBO_MINIO_USER", "admin")
        store.delete("NIMBO_MINIO_USER")
        assertTrue(store.names().isEmpty())
        assertFailsWith<InvalidParam> { store.set("NIMBO_MINIO_PASSWORD", "short") }
    }

    @Test
    fun `a failing powershell is reported without the value`() = runBlocking {
        val store = DpapiSecretStore(MemoryDataStore()) { _, _, _ -> ExecResult(1, "boom s3cret-value") }
        val e = assertFailsWith<IllegalStateException> { store.set("NIMBO_POSTGRES_PASSWORD", "s3cret-value") }
        assertFalse("s3cret-value" in e.message.orEmpty())
    }

    /** The real thing, only where it exists. Skipped silently elsewhere. */
    @Test
    fun `real DPAPI round trip on Windows`() = runBlocking {
        if (!System.getProperty("os.name").startsWith("Windows")) return@runBlocking
        val dir = Files.createTempDirectory("nimbo-dpapi").toFile()
        val store = DpapiSecretStore(FileDataStore(dir), ProcessShell("powershell.exe"))
        store.set("NIMBO_POSTGRES_PASSWORD", "päss wörd 123 \"quoted\" \$dollar")
        assertEquals("päss wörd 123 \"quoted\" \$dollar", store.get("NIMBO_POSTGRES_PASSWORD"))
        assertFalse("päss" in dir.resolve(DataFiles.SECRETS).readText())
        // Another instance over the same folder reads it back, as after a restart.
        assertEquals("päss wörd 123 \"quoted\" \$dollar", DpapiSecretStore(FileDataStore(dir), ProcessShell("powershell.exe")).get("NIMBO_POSTGRES_PASSWORD"))
    }
}
