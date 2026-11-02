package es.sebas1705.nimbo

import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption

/** Where Nimbo keeps what it learns about you: ~/.nimbo, or NIMBO_DATA_DIR (a volume in Docker). */
fun dataDir(env: Map<String, String>, home: String): File =
    env["NIMBO_DATA_DIR"]?.takeIf { it.isNotBlank() }?.let(::File) ?: File(home, ".nimbo")

/** A tiny named-file store. Names are plain file names; nothing here can reach outside its folder. */
interface DataStore {
    /** Folder on disk, or null for the in-memory store used by tests. */
    val location: File?
    fun read(name: String): String?
    fun write(name: String, text: String)
    /** Appends one line; when the file grows past [maxBytes] only its newest half is kept. */
    fun append(name: String, line: String, maxBytes: Long = 512 * 1024)
    fun delete(name: String)
    fun sizes(): Map<String, Long>
}

private val NAME = Regex("[a-z0-9][a-z0-9._-]{0,63}")
private fun checked(name: String): String {
    require(NAME.matches(name) && ".." !in name) { "invalid data file name" }
    return name
}

class FileDataStore(private val root: File) : DataStore {
    override val location: File get() = root

    override fun read(name: String): String? = File(root, checked(name)).takeIf { it.isFile }?.readText()

    @Synchronized
    override fun write(name: String, text: String) {
        root.mkdirs()
        val target = File(root, checked(name))
        val tmp = File(root, "$name.tmp")
        tmp.writeText(text)
        // Write beside and swap, so a crash leaves either the old file or the new one, never half of it.
        try {
            Files.move(tmp.toPath(), target.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        } catch (e: java.nio.file.AtomicMoveNotSupportedException) {
            Files.move(tmp.toPath(), target.toPath(), StandardCopyOption.REPLACE_EXISTING)
        }
    }

    @Synchronized
    override fun append(name: String, line: String, maxBytes: Long) {
        root.mkdirs()
        val file = File(root, checked(name))
        if (file.isFile && file.length() > maxBytes) {
            val lines = file.readLines()
            file.writeText(lines.drop(lines.size / 2).joinToString("\n", postfix = "\n"))
        }
        Files.writeString(file.toPath(), line.replace('\n', ' ') + "\n", StandardOpenOption.CREATE, StandardOpenOption.APPEND)
    }

    override fun delete(name: String) {
        File(root, checked(name)).delete()
    }

    override fun sizes(): Map<String, Long> =
        root.listFiles { f -> f.isFile && NAME.matches(f.name) }?.sortedBy { it.name }?.associate { it.name to it.length() }.orEmpty()
}

class MemoryDataStore : DataStore {
    private val files = linkedMapOf<String, String>()
    override val location: File? = null
    @Synchronized override fun read(name: String) = files[checked(name)]
    @Synchronized override fun write(name: String, text: String) { files[checked(name)] = text }
    @Synchronized override fun append(name: String, line: String, maxBytes: Long) {
        val lines = files[checked(name)].orEmpty().lines().filter { it.isNotEmpty() }.toMutableList()
        lines += line.replace('\n', ' ')
        files[name] = (if (lines.sumOf { it.length + 1 } > maxBytes) lines.drop(lines.size / 2) else lines).joinToString("\n", postfix = "\n")
    }
    @Synchronized override fun delete(name: String) { files.remove(checked(name)) }
    @Synchronized override fun sizes() = files.mapValues { it.value.length.toLong() }
}

internal val dataJson = Json { ignoreUnknownKeys = true; encodeDefaults = true; prettyPrint = true }

/** One journal entry per line, so no pretty printing. */
internal val lineJson = Json { ignoreUnknownKeys = true; encodeDefaults = true }

/** Reads a JSON document; a damaged one is set aside as `<name>.bad` and replaced by the default instead of crashing. */
internal inline fun <reified T> DataStore.readOrDefault(name: String, default: () -> T): T {
    val text = read(name) ?: return default()
    return try {
        dataJson.decodeFromString<T>(text)
    } catch (e: SerializationException) {
        write("$name.bad", text)
        default()
    } catch (e: IllegalArgumentException) {
        write("$name.bad", text)
        default()
    }
}

internal inline fun <reified T> DataStore.writeJson(name: String, value: T) = write(name, dataJson.encodeToString(value))

object DataFiles {
    const val SETTINGS = "settings.json"
    const val PROGRESS = "progress.json"
    const val STATS = "stats.json"
    const val JOURNAL = "journal.jsonl"
    const val FIDELITY = "fidelity-history.json"
    const val SECRETS = "secrets.json"
    /** Files a backup carries. Secrets are deliberately not among them. */
    val BACKUP = listOf(SETTINGS, PROGRESS, STATS, FIDELITY)
}

// ---------------------------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------------------------

@Serializable
data class Settings(
    val lang: String? = null,
    val theme: String = "auto",
    /** "auto" checks GitHub for a newer version once a day; "manual" never contacts anyone by itself. */
    val updateMode: String = "auto",
    /** Providers whose data is kept in a Docker volume between runs. */
    val persist: Set<String> = emptySet(),
    val lastUpdateCheck: Long? = null,
)

@Serializable
data class SettingsPatch(val lang: String? = null, val theme: String? = null, val updateMode: String? = null)

class SettingsService(private val store: DataStore) {
    @Synchronized fun get(): Settings = store.readOrDefault(DataFiles.SETTINGS) { Settings() }

    @Synchronized
    fun patch(p: SettingsPatch): Settings {
        p.lang?.let { if (it !in setOf("es", "en")) throw InvalidParam("lang must be es or en") }
        p.theme?.let { if (it !in setOf("auto", "light", "dark")) throw InvalidParam("theme must be auto, light or dark") }
        p.updateMode?.let { if (it !in setOf("auto", "manual")) throw InvalidParam("updateMode must be auto or manual") }
        val next = get().let { it.copy(lang = p.lang ?: it.lang, theme = p.theme ?: it.theme, updateMode = p.updateMode ?: it.updateMode) }
        store.writeJson(DataFiles.SETTINGS, next)
        return next
    }

    @Synchronized
    fun setPersist(providerId: String, enabled: Boolean): Settings {
        val def = Catalog.find(providerId) ?: throw UnknownTarget("Unknown provider '$providerId'")
        if (def.persist == null) throw InvalidParam("${def.name} keeps no data that could be preserved")
        val cur = get()
        val next = cur.copy(persist = if (enabled) cur.persist + providerId else cur.persist - providerId)
        store.writeJson(DataFiles.SETTINGS, next)
        return next
    }

    @Synchronized
    fun markUpdateCheck(at: Long) {
        store.writeJson(DataFiles.SETTINGS, get().copy(lastUpdateCheck = at))
    }

    fun persists(providerId: String): Boolean = providerId in get().persist
}

// ---------------------------------------------------------------------------------------------
// Lab progress
// ---------------------------------------------------------------------------------------------

@Serializable
data class LabRecord(val completedAt: Long? = null, val attempts: Int = 0, val firstAttemptAt: Long? = null, val lastAttemptAt: Long? = null)

@Serializable
data class Progress(val labs: Map<String, LabRecord> = emptyMap())

class ProgressService(private val store: DataStore, private val clock: () -> Long) {
    @Synchronized fun get(): Progress = store.readOrDefault(DataFiles.PROGRESS) { Progress() }

    /** Records a verification. Completion is sticky: failing later does not take the badge away. */
    @Synchronized
    fun attempt(labId: String, passed: Boolean): LabRecord {
        val now = clock()
        val prev = get().labs[labId] ?: LabRecord()
        val next = prev.copy(
            attempts = prev.attempts + 1,
            firstAttemptAt = prev.firstAttemptAt ?: now,
            lastAttemptAt = now,
            completedAt = prev.completedAt ?: if (passed) now else null,
        )
        store.writeJson(DataFiles.PROGRESS, Progress(get().labs + (labId to next)))
        return next
    }

    /** Brings over what an earlier version kept in the browser. Existing records win. */
    @Synchronized
    fun importDone(ids: Collection<String>): Progress {
        val now = clock()
        val cur = get().labs
        val added = ids.filter { it !in cur && Regex("[a-z0-9-]{1,41}").matches(it) }.associateWith { LabRecord(completedAt = now, attempts = 0) }
        val next = Progress(cur + added)
        if (added.isNotEmpty()) store.writeJson(DataFiles.PROGRESS, next)
        return next
    }

    fun completed(): Set<String> = get().labs.filterValues { it.completedAt != null }.keys
}

// ---------------------------------------------------------------------------------------------
// Usage statistics and the activity journal
// ---------------------------------------------------------------------------------------------

@Serializable
data class ProviderUsage(val starts: Int = 0, val stops: Int = 0, val upSeconds: Long = 0, val lastStartedAt: Long? = null)

@Serializable
data class StatsData(val since: Long = 0, val providers: Map<String, ProviderUsage> = emptyMap(), val counters: Map<String, Int> = emptyMap())

@Serializable
data class JournalEntry(val at: Long, val kind: String, val subject: String, val detail: String? = null)

/** Everything stays on this machine. Nothing here is ever sent anywhere. */
class StatsService(private val store: DataStore, private val clock: () -> Long) {
    @Synchronized
    fun stats(): StatsData = store.readOrDefault(DataFiles.STATS) { StatsData(since = clock()) }.let { if (it.since == 0L) it.copy(since = clock()) else it }

    @Synchronized
    fun record(kind: String, subject: String, detail: String? = null) {
        val now = clock()
        store.append(DataFiles.JOURNAL, lineJson.encodeToString(JournalEntry.serializer(), JournalEntry(now, kind, subject, detail?.take(200))))
        var s = stats()
        s = when (kind) {
            "provider.start" -> s.withProvider(subject) { it.copy(starts = it.starts + 1, lastStartedAt = now) }
            "provider.stop" -> s.withProvider(subject) { it.copy(stops = it.stops + 1) }
            else -> s
        }
        s = s.copy(counters = s.counters + (kind to ((s.counters[kind] ?: 0) + 1)))
        store.writeJson(DataFiles.STATS, s)
    }

    /** Called on a timer with the providers that are up: time is measured while Nimbo is open, not guessed. */
    @Synchronized
    fun sample(running: Collection<String>, seconds: Long) {
        if (running.isEmpty()) return
        var s = stats()
        running.forEach { id -> s = s.withProvider(id) { it.copy(upSeconds = it.upSeconds + seconds) } }
        store.writeJson(DataFiles.STATS, s)
    }

    fun journal(limit: Int = 50): List<JournalEntry> =
        (store.read(DataFiles.JOURNAL) ?: "").lineSequence().filter { it.isNotBlank() }.mapNotNull {
            try { lineJson.decodeFromString(JournalEntry.serializer(), it) } catch (e: SerializationException) { null }
        }.toList().takeLast(limit.coerceIn(1, 500)).reversed()

    @Synchronized
    fun reset() {
        store.delete(DataFiles.STATS)
        store.delete(DataFiles.JOURNAL)
    }

    private fun StatsData.withProvider(id: String, f: (ProviderUsage) -> ProviderUsage) =
        copy(providers = providers + (id to f(providers[id] ?: ProviderUsage())))
}

// ---------------------------------------------------------------------------------------------
// History of the fidelity matrix
// ---------------------------------------------------------------------------------------------

@Serializable
data class FidelityRunSummary(val at: Long, val durationMs: Long, val suites: Map<String, String>, val steps: Int, val passed: Int)

@Serializable
data class FidelityChange(val suite: String, val from: String, val to: String, val regression: Boolean)

class FidelityHistory(private val store: DataStore, private val clock: () -> Long) {
    @Synchronized fun list(): List<FidelityRunSummary> =
        store.readOrDefault(DataFiles.FIDELITY) { emptyList<FidelityRunSummary>() }

    /** What changed against the last time each suite ran. Offline suites say nothing about quality, so they never count as regressions. */
    fun changesFor(report: FidelityReportDto): List<FidelityChange> {
        val history = list()
        return report.suites.mapNotNull { s ->
            val before = history.asReversed().firstNotNullOfOrNull { it.suites[s.id] } ?: return@mapNotNull null
            if (before == s.status) null
            else FidelityChange(s.id, before, s.status, regression = before != "offline" && s.status != "offline" && rank(s.status) < rank(before))
        }
    }

    @Synchronized
    fun record(report: FidelityReportDto): FidelityRunSummary {
        val steps = report.suites.flatMap { it.steps }
        val entry = FidelityRunSummary(
            at = clock(), durationMs = report.durationMs,
            suites = report.suites.associate { it.id to it.status },
            steps = steps.size, passed = steps.count { it.status == "pass" },
        )
        store.writeJson(DataFiles.FIDELITY, (list() + entry).takeLast(100))
        return entry
    }

    @Synchronized fun clear() = store.delete(DataFiles.FIDELITY)

    private fun rank(status: String) = when (status) { "full" -> 3; "partial" -> 2; "missing" -> 1; else -> 0 }
}

/** Everything the app remembers, behind one object so tests can swap the store. */
class AppData(val store: DataStore = MemoryDataStore(), val clock: () -> Long = System::currentTimeMillis) {
    val settings = SettingsService(store)
    val progress = ProgressService(store, clock)
    val stats = StatsService(store, clock)
    val fidelity = FidelityHistory(store, clock)

    companion object {
        fun onDisk(root: File) = AppData(FileDataStore(root))
    }
}

/** A backup of what is worth keeping. Secrets are never part of it. */
@Serializable
data class DataBundle(val format: Int = 1, val exportedAt: Long, val files: Map<String, String>)

fun AppData.export(): DataBundle =
    DataBundle(exportedAt = clock(), files = DataFiles.BACKUP.mapNotNull { n -> store.read(n)?.let { n to it } }.toMap())

/** Restores a backup. Each file must parse as what it claims to be before anything is written. */
fun AppData.restore(bundle: DataBundle): List<String> {
    if (bundle.format != 1) throw InvalidParam("unsupported backup format ${bundle.format}")
    val unknown = bundle.files.keys - DataFiles.BACKUP.toSet()
    if (unknown.isNotEmpty()) throw InvalidParam("unexpected files in the backup: ${unknown.joinToString()}")
    bundle.files.forEach { (name, text) ->
        try {
            when (name) {
                DataFiles.SETTINGS -> dataJson.decodeFromString<Settings>(text)
                DataFiles.PROGRESS -> dataJson.decodeFromString<Progress>(text)
                DataFiles.STATS -> dataJson.decodeFromString<StatsData>(text)
                DataFiles.FIDELITY -> dataJson.decodeFromString(ListSerializer(FidelityRunSummary.serializer()), text)
            }
        } catch (e: SerializationException) {
            throw InvalidParam("$name in the backup is damaged")
        }
    }
    bundle.files.forEach { (name, text) -> store.write(name, text) }
    return bundle.files.keys.sorted()
}
