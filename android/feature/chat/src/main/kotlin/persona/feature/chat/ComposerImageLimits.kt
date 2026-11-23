package persona.feature.chat

import android.content.Context
import android.content.Intent
import android.content.res.Resources
import android.net.Uri
import android.os.Build
import android.os.ext.SdkExtensions
import android.provider.MediaStore
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContract
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.flow.MutableStateFlow
import persona.core.contracts.ImageLimits
import persona.core.contracts.ImageLimitsTable
import persona.core.contracts.api.DraftUploadState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.SocketHello
import persona.core.designsystem.LocalHubImageLimits

/** Videos keep their own cap of 4, independent of a message's picture limits (docs/protocol/protocol.md "Sending videos"). */
const val MAX_COMPOSER_VIDEOS = 4

/** Remaining video slots in a message; pictures and ordinary files do not count toward videos. */
fun composerVideoRoom(attachments: List<ComposerAttachment>): Int =
    (MAX_COMPOSER_VIDEOS - attachments.count { it.video && it.uploadState != DraftUploadState.Invalid }).coerceAtLeast(0)

/** Remaining inline picture slots in a message by count alone; videos and ordinary files do not count toward pictures. */
fun composerPhotoRoom(attachments: List<ComposerAttachment>, limits: ImageLimits): Int =
    limits.roomLeft(attachments.count { it.image && it.uploadState != DraftUploadState.Invalid })

/** Remaining slots for a media picker that picks both pictures and videos (including file slots pictures can spill into). */
fun composerMediaPickerRoom(attachments: List<ComposerAttachment>, limits: ImageLimits, canSpill: Boolean): Int =
    composerPhotoRoom(attachments, limits) + (if (canSpill) composerFileRoom(attachments) else 0) + composerVideoRoom(attachments)

/** Whether at least one more picture can join the message (inline, or spilled as a file when [canSpill]). */
fun composerCanAddPhoto(attachments: List<ComposerAttachment>, limits: ImageLimits, canSpill: Boolean): Boolean =
    composerPhotoRoom(attachments, limits) > 0 || (canSpill && composerFileRoom(attachments) > 0)

/**
 * One incoming photo candidate paired with its scaled JPEG byte count (used for base64 body-budget accounting, while
 * [attachment] retains the original file size in case it overflows to an ordinary file upload).
 */
data class PhotoCandidate(
    val attachment: ComposerAttachment,
    val scaledBytes: Long = attachment.size ?: 0L,
)

/** Outcome of admitting one or more incoming photos under [ImageLimits]. */
data class ComposerPhotoPlan(
    val acceptedPhotos: List<ComposerAttachment> = emptyList(),
    val spilledFiles: List<ComposerAttachment> = emptyList(),
    val refused: List<ComposerAttachment> = emptyList(),
    val spilledByCount: Int = 0,
    val spilledByBytes: Int = 0,
    val refusedByCount: Int = 0,
    val refusedByBytes: Int = 0,
) {
    /** Every attachment that joins the composer (inline photos plus overflow file attachments). */
    val added: List<ComposerAttachment> get() = acceptedPhotos + spilledFiles

    /** Localized notices explaining which limit was hit and how many pictures went as files. */
    fun notices(resources: Resources, limits: ImageLimits): List<String> = buildList {
        if (spilledByCount > 0) add(
            if (spilledByCount == 1) resources.getString(R.string.chat_composer_photos_as_files_one, limits.perMessage)
            else resources.getString(R.string.chat_composer_photos_as_files_other, limits.perMessage, spilledByCount)
        )
        if (spilledByBytes > 0) add(
            if (spilledByBytes == 1) resources.getString(R.string.chat_composer_photos_bytes_as_files_one)
            else resources.getString(R.string.chat_composer_photos_bytes_as_files_other, spilledByBytes)
        )
        if (refusedByCount > 0) add(
            if (spilledByCount > 0) resources.getString(R.string.chat_composer_file_limit)
            else resources.getString(R.string.chat_composer_photo_limit, limits.perMessage)
        )
        if (refusedByBytes > 0) add(
            if (spilledByBytes > 0) resources.getString(R.string.chat_composer_file_limit)
            else resources.getString(R.string.chat_composer_photos_too_large)
        )
    }
}

/**
 * Admits [incoming] photos against [limits] (count `per_message` and base64 budget `request_bytes - 1 MiB`), spilling
 * overflow pictures into ordinary file attachments (`image = false, video = false`) when [canSpill] and file slots remain.
 */
fun planComposerPhotos(
    existing: List<ComposerAttachment>,
    incoming: List<PhotoCandidate>,
    limits: ImageLimits,
    canSpill: Boolean,
): ComposerPhotoPlan {
    val heldBase64 = existing.asSequence()
        .filter { it.image && it.uploadState != DraftUploadState.Invalid }
        .map { ImageLimits.base64Bytes(it.size ?: 0L) }
        .toMutableList()
    var fileSlots = composerFileRoom(existing)
    val accepted = mutableListOf<ComposerAttachment>()
    val spilled = mutableListOf<ComposerAttachment>()
    val refused = mutableListOf<ComposerAttachment>()
    var spilledCount = 0
    var spilledBytes = 0
    var refusedCount = 0
    var refusedBytes = 0

    for (candidate in incoming) {
        val attachment = candidate.attachment
        if (attachment.uploadState == DraftUploadState.Invalid) {
            accepted += attachment
            continue
        }
        val nextBase64 = ImageLimits.base64Bytes(candidate.scaledBytes)
        when (val room = limits.room(heldBase64, nextBase64)) {
            ImageLimits.Room.Ok -> {
                accepted += attachment.copy(image = true, video = false, size = candidate.scaledBytes.takeIf { it > 0 } ?: attachment.size)
                heldBase64 += nextBase64
            }
            ImageLimits.Room.Count, ImageLimits.Room.Bytes -> {
                val withinFileCap = attachment.size == null || attachment.size <= MAX_COMPOSER_FILE_BYTES
                if (canSpill && fileSlots > 0 && withinFileCap) {
                    spilled += attachment.copy(image = false, video = false)
                    fileSlots--
                    if (room == ImageLimits.Room.Count) spilledCount++ else spilledBytes++
                } else {
                    refused += attachment
                    if (room == ImageLimits.Room.Count) refusedCount++ else refusedBytes++
                }
            }
        }
    }
    return ComposerPhotoPlan(accepted, spilled, refused, spilledCount, spilledBytes, refusedCount, refusedBytes)
}

/**
 * Visual media picker sized dynamically at launch time by remaining room. Uses single-pick when room is 1 because
 * [ActivityResultContracts.PickMultipleVisualMedia] requires `maxItems > 1`.
 */
class DynamicPickVisualMedia : ActivityResultContract<Pair<PickVisualMediaRequest, Int>, List<@JvmSuppressWildcards Uri>>() {
    private val single = ActivityResultContracts.PickVisualMedia()
    private val parser = ActivityResultContracts.PickMultipleVisualMedia(2)

    override fun createIntent(context: Context, input: Pair<PickVisualMediaRequest, Int>): Intent {
        val (request, room) = input
        if (room <= 1) return single.createIntent(context, request)
        val clamped = room.coerceIn(2, systemPickImagesMaxLimit())
        return ActivityResultContracts.PickMultipleVisualMedia(clamped).createIntent(context, request)
    }

    override fun parseResult(resultCode: Int, intent: Intent?): List<Uri> =
        parser.parseResult(resultCode, intent)

    companion object {
        internal fun systemPickImagesMaxLimit(): Int = runCatching {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ||
                (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R && SdkExtensions.getExtensionVersion(Build.VERSION_CODES.R) >= 2)
            ) MediaStore.getPickImagesMaxLimit().coerceAtLeast(2)
            else 100
        }.getOrDefault(100)
    }
}

/** Resolves the hub's image limits for [agent] on [connection]'s hub group, falling back to [LocalHubImageLimits]. */
@Composable
internal fun rememberSessionImageLimits(connection: HubConnection?, agent: String?): ImageLimits {
    val fallback = LocalHubImageLimits.current
    val currentHello by (connection?.currentHello ?: remember { MutableStateFlow<SocketHello?>(null) }).collectAsStateWithLifecycle()
    val table: ImageLimitsTable = currentHello?.hello?.imageLimits ?: connection?.lastHello?.imageLimits ?: fallback
    return remember(table, agent) { table.forAgent(agent) }
}
