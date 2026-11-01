package es.sebas1705.nimbo

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The in-app guides are written by hand in web/src/guides.ts. These tests keep them honest against the real
 * catalog, labs and stacks, so a renamed provider or a changed port cannot leave a guide telling people wrong things.
 */
class GuidesConsistencyTest {
    private fun repoFile(name: String) = listOf(File(name), File("../$name")).first { it.isFile }

    private val source = repoFile("web/src/guides.ts").readText()
    private val providerPart = source.substringBefore("export const RECIPES")
    private val recipePart = source.substringAfter("export const RECIPES").substringBefore("export interface TabHelp")
    private val blurbPart = source.substringAfter("export const STACK_BLURBS").substringBefore("export function stackBlurb")

    /** One block of text per provider guide, keyed by its id. */
    private val guideBlocks: Map<String, String> = providerPart
        .split("\n  {\n    id: '")
        .drop(1)
        .associate { block -> block.substringBefore("'") to block }

    @Test
    fun `every provider has a guide and every guide has a provider`() {
        assertEquals(Catalog.providers.map { it.id }.toSet(), guideBlocks.keys, "guides.ts and Catalog.kt list different providers")
    }

    @Test
    fun `the localhost ports a guide mentions are ports that provider really publishes`() {
        val chaosPorts = ChaosTargets.all.map { it.proxyPort }.toSet()
        guideBlocks.forEach { (id, block) ->
            val allowed = Catalog.find(id)!!.ports.toSet() + chaosPorts
            Regex("""localhost:(\d+)""").findAll(block).map { it.groupValues[1].toInt() }.toSet().forEach { port ->
                assertTrue(port in allowed, "$id: the guide mentions localhost:$port but the provider publishes ${Catalog.find(id)!!.ports}")
            }
        }
    }

    @Test
    fun `the endpoint a guide gives for a provider is its first published port`() {
        // the card shows the first port big, so the guide must lead with the same one
        val firstUrl = mapOf(
            "floci-aws" to 4566, "floci-azure" to 4577, "floci-gcp" to 4588, "floci-oci" to 4599, "redpanda" to 9092,
            "postgres" to 5432, "redis" to 6379, "toxiproxy" to 8474, "k3s" to 6443, "jaeger" to 16686, "minio" to 9000, "azurite" to 10000,
        )
        firstUrl.forEach { (id, port) -> assertEquals(port, Catalog.find(id)!!.ports.first(), "$id: the card would show another port than the guide") }
    }

    @Test
    fun `recipes use real providers and never need a secret, so they work on a fresh install`() {
        val providerLists = Regex("""providers: \[([^\]]*)]""").findAll(recipePart).map { m ->
            Regex("'([a-z0-9-]+)'").findAll(m.groupValues[1]).map { it.groupValues[1] }.toList()
        }.toList()
        assertTrue(providerLists.size >= 4, "could not find the recipes in guides.ts")
        providerLists.flatten().forEach { id ->
            val def = Catalog.find(id)
            assertTrue(def != null, "a recipe starts '$id', which is not in the catalog")
            assertTrue(def.secretEnv.isEmpty(), "a recipe starts '$id', which needs ${def.secretEnv.values}: recipes promise no passwords")
        }
    }

    @Test
    fun `recipes point at labs that exist`() {
        val labs = LabCatalog.load(mapOf("NIMBO_LABS_DIR" to repoFile("labs/01-first-bucket.yml").parent)).labs.map { it.dto.id }.toSet()
        val used = Regex("""lab: '([a-z0-9-]+)'""").findAll(recipePart).map { it.groupValues[1] }.toList()
        assertTrue(used.isNotEmpty())
        used.forEach { assertTrue(it in labs, "a recipe links to the lab '$it', which does not exist (labs: $labs)") }
    }

    @Test
    fun `stack blurbs exist for exactly the stacks that ship`() {
        val shipped = StackCatalog.parse(repoFile("nimbo.yml").readText()).stacks.map { it.id }.toSet()
        val blurbed = Regex("""^\s{2}'?([a-z-]+)'?: t\(""", RegexOption.MULTILINE).findAll(blurbPart).map { it.groupValues[1] }.toSet()
        assertEquals(shipped, blurbed, "nimbo.yml and the translated stack descriptions are out of sync")
    }

    @Test
    fun `every NIMBO_ variable a guide names is documented in the readme`() {
        val readme = repoFile("README.md").readText()
        val names = Regex("""NIMBO_[A-Z_]+""").findAll(source).map { it.value }.toSet()
        assertTrue(names.isNotEmpty())
        names.forEach { assertTrue(it in readme, "guides.ts mentions $it but the README does not document it") }
    }

    @Test
    fun `the secrets a guide asks for are the ones the catalog really reads`() {
        val read = Catalog.providers.flatMap { it.secretEnv.values }.toSet()
        val mentioned = Regex("""NIMBO_(?:MINIO|POSTGRES)_[A-Z_]+""").findAll(source).map { it.value }.toSet()
        assertTrue(mentioned.isNotEmpty())
        assertTrue(read.containsAll(mentioned), "a guide names a secret variable the backend does not read: ${mentioned - read}")
    }
}
