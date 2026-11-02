package es.sebas1705.nimbo

import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private fun tempDir(): File = Files.createTempDirectory("nimbo-data-test").toFile().also { it.deleteOnExit() }

private fun report(vararg suites: Pair<String, String>, at: Long = 1000) = FidelityReportDto(
    startedAt = at, durationMs = 50,
    suites = suites.map { (id, status) ->
        SuiteResultDto(id, "aws", id, status, listOf(FitStepDto("a", if (status == "full") "pass" else "fail")), 5)
    },
    summary = emptyMap(),
)

class DataDirTest {
    @Test
    fun `data folder defaults to the home folder and can be moved`() {
        assertEquals(File("C:/Users/x", ".nimbo"), dataDir(emptyMap(), "C:/Users/x"))
        assertEquals(File("/data"), dataDir(mapOf("NIMBO_DATA_DIR" to "/data"), "/home/x"))
        assertEquals(File("/home/x", ".nimbo"), dataDir(mapOf("NIMBO_DATA_DIR" to "  "), "/home/x"))
    }
}

class FileDataStoreTest {
    @Test
    fun `write then read round trips and leaves no temporary file`() {
        val dir = tempDir()
        val store = FileDataStore(dir)
        assertNull(store.read("a.json"))
        store.write("a.json", "one")
        store.write("a.json", "two")
        assertEquals("two", store.read("a.json"))
        assertEquals(listOf("a.json"), dir.list()!!.toList())
        store.delete("a.json")
        assertNull(store.read("a.json"))
    }

    @Test
    fun `names cannot leave the folder`() {
        val store = FileDataStore(tempDir())
        listOf("../x", "a/b", "..", "A.json", "", ".hidden").forEach {
            assertFailsWith<IllegalArgumentException>(it) { store.write(it, "x") }
        }
        assertFailsWith<IllegalArgumentException> { MemoryDataStore().read("../x") }
    }

    @Test
    fun `append keeps newest half when the file grows too much`() {
        val store = FileDataStore(tempDir())
        repeat(100) { store.append("j.jsonl", "line-$it", maxBytes = 200) }
        val lines = store.read("j.jsonl")!!.lines().filter { it.isNotEmpty() }
        assertTrue(lines.size < 100, "should have been trimmed")
        assertEquals("line-99", lines.last())
        assertTrue(lines.zipWithNext().all { (a, b) -> a.removePrefix("line-").toInt() < b.removePrefix("line-").toInt() })
    }

    @Test
    fun `append flattens newlines so every entry is one line`() {
        val store = MemoryDataStore()
        store.append("j.jsonl", "a\nb")
        assertEquals("a b\n", store.read("j.jsonl"))
    }

    @Test
    fun `sizes list only data files`() {
        val dir = tempDir()
        val store = FileDataStore(dir)
        store.write("a.json", "12345")
        File(dir, "Not Valid.txt").writeText("x")
        assertEquals(mapOf("a.json" to 5L), store.sizes())
    }

    @Test
    fun `a damaged file is set aside and replaced by the default`() {
        val store = MemoryDataStore()
        store.write(DataFiles.SETTINGS, "{ not json")
        val settings = SettingsService(store).get()
        assertEquals(Settings(), settings)
        assertEquals("{ not json", store.read("${DataFiles.SETTINGS}.bad"))
    }
}

class SettingsTest {
    @Test
    fun `defaults and patches`() {
        val s = SettingsService(MemoryDataStore())
        assertEquals(Settings(), s.get())
        val next = s.patch(SettingsPatch(lang = "en", theme = "dark"))
        assertEquals("en", next.lang)
        assertEquals("dark", next.theme)
        assertEquals("auto", next.updateMode)
        // A later patch leaves what it does not mention alone.
        assertEquals("en", s.patch(SettingsPatch(updateMode = "manual")).lang)
    }

    @Test
    fun `invalid values are refused and nothing is written`() {
        val store = MemoryDataStore()
        val s = SettingsService(store)
        assertFailsWith<InvalidParam> { s.patch(SettingsPatch(lang = "fr")) }
        assertFailsWith<InvalidParam> { s.patch(SettingsPatch(theme = "neon")) }
        assertFailsWith<InvalidParam> { s.patch(SettingsPatch(updateMode = "sometimes")) }
        assertNull(store.read(DataFiles.SETTINGS))
    }

    @Test
    fun `persistence is per provider and only for providers that keep data`() {
        val s = SettingsService(MemoryDataStore())
        assertTrue(s.setPersist("postgres", true).persist.contains("postgres"))
        assertTrue(s.persists("postgres"))
        assertFalse(s.persists("redis"))
        assertFalse(s.setPersist("postgres", false).persist.contains("postgres"))
        assertFailsWith<UnknownTarget> { s.setPersist("nope", true) }
        val stateless = Catalog.providers.first { it.persist == null }
        assertFailsWith<InvalidParam> { s.setPersist(stateless.id, true) }
    }

    @Test
    fun `survives a restart`() {
        val dir = tempDir()
        SettingsService(FileDataStore(dir)).patch(SettingsPatch(lang = "es"))
        assertEquals("es", SettingsService(FileDataStore(dir)).get().lang)
    }
}

class ProgressTest {
    private var now = 100L
    private fun service() = ProgressService(MemoryDataStore()) { now }

    @Test
    fun `an attempt is counted and completion is sticky`() {
        val p = service()
        val first = p.attempt("lab-1", false)
        assertEquals(1, first.attempts)
        assertNull(first.completedAt)
        now = 200
        val done = p.attempt("lab-1", true)
        assertEquals(200L, done.completedAt)
        assertEquals(100L, done.firstAttemptAt)
        now = 300
        val later = p.attempt("lab-1", false)
        assertEquals(200L, later.completedAt, "failing later must not take the badge away")
        assertEquals(3, later.attempts)
        assertEquals(setOf("lab-1"), p.completed())
    }

    @Test
    fun `importing browser progress keeps existing records and rejects odd ids`() {
        val p = service()
        p.attempt("lab-1", false)
        val result = p.importDone(listOf("lab-1", "lab-2", "../etc", "UPPER", ""))
        assertEquals(setOf("lab-1", "lab-2"), result.labs.keys)
        assertNull(result.labs.getValue("lab-1").completedAt, "an existing record wins over the import")
        assertNotNull(result.labs.getValue("lab-2").completedAt)
        assertEquals(setOf("lab-2"), p.completed())
    }
}

class StatsTest {
    private var now = 1_000L
    private fun service(store: DataStore = MemoryDataStore()) = StatsService(store) { now }

    @Test
    fun `starts and stops are counted per provider`() {
        val s = service()
        s.record("provider.start", "redis")
        s.record("provider.start", "redis")
        s.record("provider.stop", "redis")
        s.record("lab.verify", "x")
        val stats = s.stats()
        assertEquals(2, stats.providers.getValue("redis").starts)
        assertEquals(1, stats.providers.getValue("redis").stops)
        assertEquals(2, stats.counters["provider.start"])
        assertEquals(1, stats.counters["lab.verify"])
        assertEquals(1_000L, stats.since)
    }

    @Test
    fun `uptime is added only for running providers`() {
        val s = service()
        s.sample(emptyList(), 15)
        assertTrue(s.stats().providers.isEmpty())
        s.sample(listOf("redis", "minio"), 15)
        s.sample(listOf("redis"), 15)
        assertEquals(30L, s.stats().providers.getValue("redis").upSeconds)
        assertEquals(15L, s.stats().providers.getValue("minio").upSeconds)
    }

    @Test
    fun `journal is newest first, limited, and tolerates a damaged line`() {
        val store = MemoryDataStore()
        val s = service(store)
        repeat(5) { now += 1; s.record("provider.start", "p$it") }
        store.append(DataFiles.JOURNAL, "{broken")
        val latest = s.journal(3)
        assertEquals(listOf("p4", "p3", "p2"), latest.map { it.subject })
        assertEquals(5, s.journal(100).size)
    }

    @Test
    fun `details are shortened`() {
        val s = service()
        s.record("x", "y", "d".repeat(1000))
        assertEquals(200, s.journal().single().detail!!.length)
    }

    @Test
    fun `reset forgets everything`() {
        val s = service()
        s.record("provider.start", "redis")
        s.reset()
        assertTrue(s.stats().providers.isEmpty())
        assertTrue(s.journal().isEmpty())
    }
}

class FidelityHistoryTest {
    private fun history() = FidelityHistory(MemoryDataStore()) { 5L }

    @Test
    fun `first run has nothing to compare with`() {
        val h = history()
        assertTrue(h.changesFor(report("s3" to "full")).isEmpty())
    }

    @Test
    fun `a step down is a regression and a step up is not`() {
        val h = history()
        h.record(report("s3" to "full", "sqs" to "partial"))
        val changes = h.changesFor(report("s3" to "partial", "sqs" to "full")).associateBy { it.suite }
        assertTrue(changes.getValue("s3").regression)
        assertFalse(changes.getValue("sqs").regression)
        assertTrue(h.changesFor(report("s3" to "full", "sqs" to "partial")).isEmpty())
    }

    @Test
    fun `an emulator that is offline is never a regression`() {
        val h = history()
        h.record(report("s3" to "full"))
        val change = h.changesFor(report("s3" to "offline")).single()
        assertFalse(change.regression)
    }

    @Test
    fun `history is capped and summarises steps`() {
        val h = history()
        repeat(120) { h.record(report("s3" to "full")) }
        assertEquals(100, h.list().size)
        val one = h.list().last()
        assertEquals(1, one.steps)
        assertEquals(1, one.passed)
        h.clear()
        assertTrue(h.list().isEmpty())
    }
}

class BackupTest {
    @Test
    fun `export then restore into a fresh store gives the same data`() {
        val a = AppData(MemoryDataStore()) { 7L }
        a.settings.patch(SettingsPatch(lang = "en"))
        a.progress.attempt("lab-1", true)
        a.stats.record("provider.start", "redis")
        a.fidelity.record(report("s3" to "full"))
        a.store.write(DataFiles.SECRETS, "{\"values\":{\"NIMBO_MINIO_PASSWORD\":\"cipher\"}}")

        val bundle = a.export()
        assertFalse(DataFiles.SECRETS in bundle.files, "secrets must never be exported")

        val b = AppData(MemoryDataStore()) { 7L }
        assertEquals(DataFiles.BACKUP.sorted(), b.restore(bundle))
        assertEquals("en", b.settings.get().lang)
        assertEquals(setOf("lab-1"), b.progress.completed())
        assertEquals(1, b.stats.stats().providers.getValue("redis").starts)
        assertEquals(1, b.fidelity.list().size)
        assertNull(b.store.read(DataFiles.SECRETS))
    }

    @Test
    fun `a bad backup writes nothing`() {
        val d = AppData(MemoryDataStore())
        d.settings.patch(SettingsPatch(lang = "es"))
        val bad = DataBundle(exportedAt = 1, files = mapOf(DataFiles.SETTINGS to "{\"lang\":\"en\"}", DataFiles.PROGRESS to "[[["))
        assertFailsWith<InvalidParam> { d.restore(bad) }
        assertEquals("es", d.settings.get().lang)
        assertFailsWith<InvalidParam> { d.restore(DataBundle(format = 2, exportedAt = 1, files = emptyMap())) }
        assertFailsWith<InvalidParam> { d.restore(DataBundle(exportedAt = 1, files = mapOf(DataFiles.SECRETS to "{}"))) }
        assertFailsWith<InvalidParam> { d.restore(DataBundle(exportedAt = 1, files = mapOf("../evil.json" to "{}"))) }
    }
}
