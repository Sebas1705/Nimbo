package es.sebas1705.nimbo

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File

@Serializable data class PackDef(val id: String, val images: List<String>)
@Serializable private data class PackFileJson(val packs: List<PackDef>)

@Serializable data class ImageState(val provider: String, val image: String, val present: Boolean, val pack: String?)
/** [verified] is null when no SHA256SUMS sits beside the file, true when it matched and false when it did not. */
@Serializable data class PackFile(val name: String, val path: String, val size: Long, val pack: String?, val verified: Boolean?)
@Serializable data class ImagesStatus(val docker: Boolean, val images: List<ImageState>, val packs: List<PackDef>, val files: List<PackFile>, val folders: List<String>)
@Serializable data class LoadImagesDto(val path: String)
@Serializable data class PullImageDto(val provider: String)
@Serializable data class ImagesLoadedDto(val loaded: List<String>)

/**
 * The container images the emulators run from. Online they are pulled on first start; offline they come from image
 * packs (`docker save` files published with each release, or made by you) loaded with `docker load`.
 */
class Images(
    private val exec: Exec,
    private val folders: () -> List<File>,
    private val data: AppData,
    private val packs: List<PackDef> = defaultPacks(),
) {
    private val lock = Mutex()

    suspend fun status(): ImagesStatus {
        val local = exec.run(listOf("images", "--format", "{{.Repository}}:{{.Tag}}"), 30)
        val have = if (local.ok) local.output.lines().map { it.trim() }.toSet() else emptySet()
        val images = Catalog.providers.map { p ->
            ImageState(p.id, p.image, normalize(p.image) in have, packs.firstOrNull { p.image in it.images }?.id)
        }
        return ImagesStatus(local.ok, images, packs, scan(), folders().map { it.path })
    }

    /** Pack files found in the usual folders. Nothing outside them is listed, but [load] accepts a path you type. */
    fun scan(): List<PackFile> = folders().distinct().flatMap { dir ->
        val sums = File(dir, "SHA256SUMS").takeIf { it.isFile }?.let { Updater.parseSums(it.readText()) }.orEmpty()
        dir.listFiles { f -> f.isFile && isPack(f.name) }.orEmpty().sortedBy { it.name }.map { f ->
            PackFile(f.name, f.path, f.length(), packOf(f.name), sums[f.name]?.let { it.equals(Updater.sha256(f), ignoreCase = true) })
        }
    }

    suspend fun load(path: String): ImagesLoadedDto = lock.withLock {
        val file = File(path)
        if (!file.isFile) throw InvalidParam("No file at that path")
        if (!isPack(file.name)) throw InvalidParam("An image pack is a .tar, .tar.gz or .tgz file made with docker save")
        val expected = File(file.parentFile, "SHA256SUMS").takeIf { it.isFile }?.let { Updater.parseSums(it.readText())[file.name] }
        if (expected != null && !expected.equals(Updater.sha256(file), ignoreCase = true)) {
            throw UpdateError("${file.name} does not match the checksum next to it, so it was not loaded")
        }
        val r = exec.run(listOf("load", "-i", file.path), 1800)
        if (!r.ok) throw UpdateError(r.output.ifBlank { "docker load failed" }.take(500))
        val loaded = r.output.lines().mapNotNull { Regex("Loaded image(?: ID)?: (.+)").matchEntire(it.trim())?.groupValues?.get(1) }
        data.stats.record("images.load", file.name, "${loaded.size} images" + if (expected != null) ", verified" else ", not verified")
        ImagesLoadedDto(loaded)
    }

    suspend fun pull(providerId: String): Unit = lock.withLock {
        val def = Catalog.find(providerId) ?: throw UnknownTarget("Unknown provider '$providerId'")
        val r = exec.run(listOf("pull", def.image), 1800)
        if (!r.ok) throw UpdateError(r.output.ifBlank { "docker pull failed" }.take(500))
        data.stats.record("images.pull", providerId)
    }

    companion object {
        fun defaultPacks(): List<PackDef> =
            Json { ignoreUnknownKeys = true }.decodeFromString<PackFileJson>(Images::class.java.getResource("/image-packs.json")!!.readText()).packs

        private val PACK = Regex(".+\\.(tar|tar\\.gz|tgz)", RegexOption.IGNORE_CASE)
        fun isPack(name: String) = PACK.matches(name) && "/" !in name && "\\" !in name

        /** `Nimbo-0.3.0-images-data.tar.gz` -> `data`. */
        fun packOf(name: String): String? = Regex("Nimbo-.+-images-([a-z0-9]+)\\.(?:tar|tar\\.gz|tgz)").matchEntire(name)?.groupValues?.get(1)

        /** `docker images` always prints a tag, so an image without one means `latest`. */
        fun normalize(ref: String): String = if (':' in ref.substringAfterLast('/')) ref else "$ref:latest"

        /** Where packs are looked for: NIMBO_IMAGES_DIR, an `images` folder beside the app, and the data folder. */
        fun searchFolders(env: Map<String, String>, data: AppData, appDir: File?): List<File> =
            listOfNotNull(env["NIMBO_IMAGES_DIR"]?.takeIf { it.isNotBlank() }?.let(::File), appDir?.let { File(it, "images") }, data.store.location?.let { File(it, "images") })
    }
}
