package es.sebas1705.nimbo

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.Serializable
import java.security.SecureRandom
import java.util.Base64

/**
 * Where the passwords of MinIO and PostgreSQL live when you do not want them in an environment variable or a .env
 * file. A store that cannot protect them says so instead of keeping them in the clear.
 */
interface SecretStore {
    val available: Boolean
    /** Why it is not available, in a sentence a person can act on. */
    val reason: String?
    /** Names that have a value. Never the values. */
    suspend fun names(): Set<String>
    suspend fun get(name: String): String?
    suspend fun set(name: String, value: String)
    suspend fun delete(name: String)
}

object Secrets {
    /** The only secrets Nimbo knows what to do with: the ones providers read from the environment. */
    val NAMES: List<String> = Catalog.providers.flatMap { it.secretEnv.values }.distinct()

    private val CONTROL = Regex("[\\p{Cntrl}]")

    fun validate(name: String, value: String) {
        if (name !in NAMES) throw UnknownTarget("Unknown secret '$name'")
        if (value.isEmpty() || value.length > 128) throw InvalidParam("The value must have between 1 and 128 characters")
        if (CONTROL.containsMatchIn(value)) throw InvalidParam("The value cannot contain control characters")
        // MinIO refuses to start with a short root password, and it is better to hear it now than from a failed container.
        if (name == "NIMBO_MINIO_PASSWORD" && value.length < 8) throw InvalidParam("MinIO needs a password of at least 8 characters")
        if (name == "NIMBO_MINIO_USER" && value.length < 3) throw InvalidParam("MinIO needs a user of at least 3 characters")
    }

    fun isPassword(name: String) = name.endsWith("PASSWORD")

    /** 24 characters from a 56-letter alphabet without look-alikes (no 0/O, 1/l/I). About 139 bits. */
    fun generate(random: SecureRandom = SecureRandom()): String {
        val alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
        return buildString { repeat(24) { append(alphabet[random.nextInt(alphabet.length)]) } }
    }

    /** Which providers read each secret, for the UI. */
    fun usedBy(name: String): List<String> = Catalog.providers.filter { name in it.secretEnv.values }.map { it.id }

    /**
     * The environment a provider is started with: the process environment first (Doppler, .env, docker compose), and
     * the secret store for whatever it leaves unset. Only the secrets that provider reads are decrypted.
     */
    suspend fun envFor(def: ProviderDef, hostEnv: Map<String, String>, store: SecretStore): Map<String, String> {
        val missing = def.secretEnv.values.filter { hostEnv[it].isNullOrBlank() }
        if (missing.isEmpty() || !store.available) return hostEnv
        val stored = missing.mapNotNull { n -> store.get(n)?.let { n to it } }.toMap()
        return hostEnv + stored
    }
}

/** Used where nothing can protect a secret: the answer to "can I store this?" is an honest no. */
class UnavailableSecretStore(override val reason: String) : SecretStore {
    override val available = false
    override suspend fun names(): Set<String> = emptySet()
    override suspend fun get(name: String): String? = null
    override suspend fun set(name: String, value: String) = throw InvalidParam(reason)
    override suspend fun delete(name: String) {}
}

/** Windows DPAPI, bound to the current Windows user. The ciphertext is a file in the data folder. */
class DpapiSecretStore(private val store: DataStore, private val shell: Shell) : SecretStore {
    private val lock = Mutex()
    override val available = true
    override val reason: String? = null

    private val entropy = "Nimbo.secrets.v1"

    @Serializable
    private data class Blobs(val values: Map<String, String> = emptyMap())

    private fun blobs(): Blobs = store.readOrDefault(DataFiles.SECRETS) { Blobs() }

    override suspend fun names(): Set<String> = blobs().values.keys.filter { it in Secrets.NAMES }.toSet()

    override suspend fun get(name: String): String? {
        val blob = blobs().values[name] ?: return null
        val plain = run("[Convert]::ToBase64String([Security.Cryptography.ProtectedData]::Unprotect(\$b, \$e, 'CurrentUser'))", blob)
        return String(Base64.getDecoder().decode(plain), Charsets.UTF_8)
    }

    override suspend fun set(name: String, value: String) {
        Secrets.validate(name, value)
        val blob = run("[Convert]::ToBase64String([Security.Cryptography.ProtectedData]::Protect(\$b, \$e, 'CurrentUser'))", Base64.getEncoder().encodeToString(value.toByteArray(Charsets.UTF_8)))
        lock.withLock { store.writeJson(DataFiles.SECRETS, Blobs(blobs().values + (name to blob))) }
    }

    override suspend fun delete(name: String) {
        lock.withLock { store.writeJson(DataFiles.SECRETS, Blobs(blobs().values - name)) }
    }

    /** The value goes in through standard input, so it never appears in a process list or a log. */
    private suspend fun run(operation: String, base64In: String): String {
        val script = "\$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Security; " +
            "\$b=[Convert]::FromBase64String([Console]::In.ReadToEnd().Trim()); " +
            "\$e=[Text.Encoding]::UTF8.GetBytes('$entropy'); $operation"
        val r = shell.run(listOf("-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script), base64In, 30)
        if (!r.ok) throw IllegalStateException("Windows could not protect or read the secret")
        return r.output.lines().last { it.isNotBlank() }.trim()
    }
}
