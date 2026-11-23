package persona.feature.chat

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.core.content.FileProvider
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.ImageLimits
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.AssistantSnapshot
import persona.feature.chat.media.ChatMedia
import persona.feature.chat.media.chatViewerActions
import java.io.File

/**
 * Verifies #13043 across `:feature:chat`: hub `image_limits` for count & base64 body budget, overflow to file
 * attachments with localized notices, independent video limit, JPEG scaling to `max_side`, drop import, forwarding,
 * viewer limit, and en/zh/ja strings.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ComposerImageLimitsTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val created = mutableListOf<File>()

    @Before fun setUpProvider() {
        useChatFileProvider(context)
    }

    @After fun cleanUp() {
        created.forEach(File::delete)
        File(context.cacheDir, "chat-drops").deleteRecursively()
        File(context.cacheDir, "chat-captures").deleteRecursively()
    }

    private fun photo(id: String, size: Long = 1024L) =
        ComposerAttachment(id, "$id.jpg", "content://example/$id", image = true, size = size)

    private fun file(id: String) =
        ComposerAttachment(id, "$id.txt", "content://example/$id", image = false, size = 128L)

    private fun video(id: String) =
        ComposerAttachment(id, "$id.mp4", "content://example/$id", image = false, video = true, size = 4096L)

    @Test fun forwardAndAttachAllowMoreThanFourPhotosUnderDefaultHubLimits() = runTest {
        val paths = List(6) { "/tmp/photo-$it.jpg" }
        val raw = "Look\n\n<attached_images>\n${paths.joinToString("\n")}\n</attached_images>"
        val session = Session("box", "s1", title = "Work", cwd = "/work", agent = "claude")
        val machine = Machine("box", "Box", online = true)
        val forwarding = ChatForwarding(
            agents = MutableStateFlow(listOf(session)),
            machines = MutableStateFlow(listOf(machine)),
            assistant = MutableStateFlow(AssistantSnapshot(null, emptyList())),
            personaGroupOf = { true },
            conversationTitle = { it.title },
            memory = { null },
            openChat = { _, _ -> },
            openPersonaHome = {},
            openNewAgent = {},
        )
        val draft = forwardDraftOf(MessageTarget("m1", "Look", user = true, raw = raw), session.key, session, machine, ChatRenderStyle.Agent, forwarding)
        assertEquals(6, draft.photos.size)
        val capped = forwardDraftOf(MessageTarget("m1", "Look", user = true, raw = raw), session.key, session, machine, ChatRenderStyle.Agent, forwarding, maxPhotos = 3)
        assertEquals(3, capped.photos.size)

        val composer = ChatComposerState()
        val handoff = RestoredPhotos(List(6) { index ->
            ComposerAttachment("p$index", "photo-$index.jpg", "", image = true, size = 1024)
        })
        val rejected = attachForwardedPhotos(context, composer, listOf(handoff))
        assertTrue(rejected.isEmpty())
        assertEquals(6, composer.attachments.count { it.image })
    }

    @Test fun dropImportAcceptsFifthPhotoAndSpillsPastLimitToFile() {
        val imageFile = File(File(context.cacheDir, "chat-captures").apply { mkdirs() }, "fifth.png")
        val bitmap = Bitmap.createBitmap(4, 4, Bitmap.Config.ARGB_8888)
        imageFile.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
        created += imageFile
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.chat.files", imageFile)

        val fourPhotos = List(4) { photo("p$it") }
        val fifth = importDroppedAttachment(context, uri, existing = fourPhotos)
        assertNotNull("5th photo must not be rejected at legacy cap 4", fifth.attachment)
        assertTrue(fifth.attachment!!.image)
        assertNull(fifth.notice)

        val tightLimits = ImageLimits(perMessage = 2)
        val spilled = importDroppedAttachment(context, uri, existing = List(2) { photo("e$it") }, limits = tightLimits, canSpill = true)
        assertNotNull(spilled.attachment)
        assertFalse("overflow picture becomes an ordinary file attachment", spilled.attachment!!.image)
        assertFalse(spilled.attachment!!.video)
        assertNotNull(spilled.notice)

        val refused = importDroppedAttachment(context, uri, existing = List(2) { photo("e$it") }, limits = tightLimits, canSpill = false)
        assertNull(refused.attachment)
        assertNotNull(refused.notice)
    }

    @Config(qualifiers = "en")
    @Test fun planComposerPhotosSpillsCountAndBytesOverflowAndReportsWhichLimitWasHit() {
        val countLimits = ImageLimits(perMessage = 2)
        val countPlan = planComposerPhotos(
            existing = listOf(photo("p0")),
            incoming = List(3) { PhotoCandidate(photo("n$it"), scaledBytes = 1024L) },
            limits = countLimits,
            canSpill = true,
        )
        assertEquals(1, countPlan.acceptedPhotos.size)
        assertEquals(2, countPlan.spilledFiles.size)
        assertTrue(countPlan.spilledFiles.none { it.image || it.video })
        assertTrue(countPlan.refused.isEmpty())
        assertEquals(
            listOf("A message holds up to 2 pictures. The other 2 will go as files."),
            countPlan.notices(context.resources, countLimits),
        )

        // Base64 budget: request_bytes - 1 MiB = 400 bytes -> 150 raw bytes = 200 base64 bytes each -> 2 fit, 3rd spills.
        val bytesLimits = ImageLimits(requestBytes = ImageLimits.BODY_ROOM + 400L, perMessage = 20)
        val bytesPlan = planComposerPhotos(
            existing = listOf(photo("e0", size = 150L)),
            incoming = listOf(
                PhotoCandidate(photo("n0", size = 5000L), scaledBytes = 150L),
                PhotoCandidate(photo("n1", size = 5000L), scaledBytes = 150L),
            ),
            limits = bytesLimits,
            canSpill = true,
        )
        assertEquals(1, bytesPlan.acceptedPhotos.size)
        assertEquals(150L, bytesPlan.acceptedPhotos.single().size)
        assertEquals(1, bytesPlan.spilledFiles.size)
        assertEquals("spilled file retains original size", 5000L, bytesPlan.spilledFiles.single().size)
        assertEquals(
            listOf("These pictures are too large together. One of them will go as a file."),
            bytesPlan.notices(context.resources, bytesLimits),
        )

        // Without a computer (canSpill = false), overflow photos are refused with the exact limit notice.
        val refusedBytes = planComposerPhotos(
            existing = listOf(photo("e0", size = 300L)),
            incoming = listOf(PhotoCandidate(photo("n0", size = 150L), scaledBytes = 150L)),
            limits = bytesLimits,
            canSpill = false,
        )
        assertTrue(refusedBytes.added.isEmpty())
        assertEquals(1, refusedBytes.refused.size)
        assertEquals(
            listOf("These pictures are too large to send together"),
            refusedBytes.notices(context.resources, bytesLimits),
        )
    }

    @Test fun videosKeepTheirOwnCapOfFourAndPickerSizesByRemainingRoom() {
        val limits = ImageLimits(perMessage = 5)
        val existing = List(4) { video("v$it") } + listOf(photo("p0"), photo("p1")) + List(3) { file("f$it") }
        assertEquals(0, composerVideoRoom(existing))
        assertEquals("videos must not eat into photo slots", 3, composerPhotoRoom(existing, limits))
        assertEquals(7, composerFileRoom(existing))
        assertEquals(10, composerMediaPickerRoom(existing, limits, canSpill = true))
        assertEquals(3, composerMediaPickerRoom(existing, limits, canSpill = false))
    }

    @Test fun encodePhotoJpegScalesToHubMaxSideAtQuality75() {
        val big = Bitmap.createBitmap(2000, 1000, Bitmap.Config.ARGB_8888)
        try {
            val encoded = encodePhotoJpeg(big, ImageLimits(maxSide = 800))
            assertTrue(encoded.isNotEmpty())
            val decoded = BitmapFactory.decodeByteArray(encoded, 0, encoded.size)
            assertNotNull(decoded)
            assertEquals(800, decoded.width)
            assertEquals(400, decoded.height)
            decoded.recycle()
        } finally {
            big.recycle()
        }
    }

    @Config(qualifiers = "en")
    @Test fun viewerLimitStringAndEnglishResourcesMatchHubLimits() = runTest {
        val media = object : ChatMedia {
            override suspend fun save(bytes: ByteArray) = true
            override suspend fun share(bytes: ByteArray) = true
            override suspend fun copy(bytes: ByteArray) = true
        }
        val edit = chatViewerActions(context.resources, media, { true }, { null }).first { it.label == "Edit" }
        assertEquals("Up to 20 photos at a time", edit.onClick(0, byteArrayOf(1)))
        val customEdit = chatViewerActions(context.resources, media, { true }, { null }, maxPhotos = 7).first { it.label == "Edit" }
        assertEquals("Up to 7 photos at a time", customEdit.onClick(0, byteArrayOf(1)))

        assertEquals("Up to 20 photos at a time", context.getString(R.string.chat_composer_photo_limit, 20))
        assertEquals("Up to 4 videos at a time", context.getString(R.string.chat_composer_video_limit, 4))
        assertEquals("These pictures are too large to send together", context.getString(R.string.chat_composer_photos_too_large))
        assertEquals("A message holds up to 20 pictures. The other one will go as a file.", context.getString(R.string.chat_composer_photos_as_files_one, 20))
        assertEquals("A message holds up to 20 pictures. The other 3 will go as files.", context.getString(R.string.chat_composer_photos_as_files_other, 20, 3))
        assertEquals("These pictures are too large together. One of them will go as a file.", context.getString(R.string.chat_composer_photos_bytes_as_files_one))
        assertEquals("These pictures are too large together. 2 of them will go as files.", context.getString(R.string.chat_composer_photos_bytes_as_files_other, 2))
    }

    @Config(qualifiers = "zh")
    @Test fun chineseImageLimitStringsFormatCleanly() {
        assertEquals("一次最多 20 张", context.getString(R.string.chat_composer_photo_limit, 20))
        assertEquals("一次最多 4 个视频", context.getString(R.string.chat_composer_video_limit, 4))
        assertEquals("这些图加起来太大了，没法一起发送", context.getString(R.string.chat_composer_photos_too_large))
        assertEquals("一条消息最多 20 张图，另外 1 张作为文件发送", context.getString(R.string.chat_composer_photos_as_files_one, 20))
        assertEquals("一条消息最多 20 张图，另外 3 张作为文件发送", context.getString(R.string.chat_composer_photos_as_files_other, 20, 3))
        assertEquals("这些图加起来太大了，其中 1 张作为文件发送", context.getString(R.string.chat_composer_photos_bytes_as_files_one))
        assertEquals("这些图加起来太大了，其中 2 张作为文件发送", context.getString(R.string.chat_composer_photos_bytes_as_files_other, 2))
    }

    @Config(qualifiers = "ja")
    @Test fun japaneseImageLimitStringsFormatCleanly() {
        assertEquals("一度に20枚まで", context.getString(R.string.chat_composer_photo_limit, 20))
        assertEquals("動画は一度に4本まで", context.getString(R.string.chat_composer_video_limit, 4))
        assertEquals("画像の合計が大きすぎて、まとめて送れません", context.getString(R.string.chat_composer_photos_too_large))
        assertEquals("1件のメッセージに入れられる画像は20枚までです。残りの1枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_as_files_one, 20))
        assertEquals("1件のメッセージに入れられる画像は20枚までです。残りの3枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_as_files_other, 20, 3))
        assertEquals("画像の合計が大きすぎます。1枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_bytes_as_files_one))
        assertEquals("画像の合計が大きすぎます。2枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_bytes_as_files_other, 2))
    }
}
