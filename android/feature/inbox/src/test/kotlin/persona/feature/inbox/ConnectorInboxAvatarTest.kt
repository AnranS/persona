package persona.feature.inbox

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.annotation.DelicateCoilApi
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.PersonaTheme
import persona.core.domain.inbox.InboxItem
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.lang.reflect.Proxy
import java.util.concurrent.atomic.AtomicInteger

/** A connector row uses the same immediate initial, fixed layout and loaded-picture cache as a Lark row. */
@OptIn(DelicateCoilApi::class)
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ConnectorInboxAvatarTest {
    @get:Rule val rule = createComposeRule()

    private lateinit var pictures: ImageLoader
    private var loading: CompletableDeferred<ByteArray>? = null
    private val picture: ByteArray by lazy {
        val bitmap = Bitmap.createBitmap(16, 16, Bitmap.Config.ARGB_8888).apply { eraseColor(android.graphics.Color.MAGENTA) }
        ByteArrayOutputStream().also { out ->
            check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, out))
        }.toByteArray()
    }

    @Before fun installPictures() {
        pictures = ImageLoader.Builder(ApplicationProvider.getApplicationContext<Context>())
            .coroutineContext(Dispatchers.Unconfined)
            .components {
                // Robolectric's static decoder rejects real PNG bytes; use the Android bitmap decoder instead.
                add(coil3.decode.BitmapFactoryDecoder.Factory())
            }.build()
        SingletonImageLoader.setUnsafe(pictures)
    }

    @After fun releasePictures() {
        loading?.complete(picture)
        SingletonImageLoader.reset()
        pictures.shutdown()
    }

    private val repository = Proxy.newProxyInstance(InboxRepository::class.java.classLoader, arrayOf(InboxRepository::class.java)) { proxy, method, args ->
        when (method.name) {
            "equals" -> proxy === args[0]
            "hashCode" -> System.identityHashCode(proxy)
            "toString" -> "ConnectorAvatarRepository"
            else -> error("Unexpected repository access: ${method.name}")
        }
    } as InboxRepository
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private fun record(avatar: AuthenticatedLarkImage?, id: String = "telegram:1", title: String = "Connector conversation",
        sender: String = "Zoe Example") = InboxRecord(
        InboxItem(id, kind = "dm", title = title, sender = sender, preview = "Latest message"),
        source = "telegram", connector = InboxConnectorRef("u1"), avatar = avatar,
    )

    private fun row(title: String = "Connector conversation") = rule.onNode(hasText(title) and hasClickAction())
    private fun bounds(title: String = "Connector conversation") = row(title).fetchSemanticsNode().boundsInRoot
    private fun initial(letter: String = "Z") = rule.onNodeWithText(letter, useUnmergedTree = true)

    @Test fun loadedPictureKeepsTheInitialAndRowInPlaceThenSurvivesRecompositionAndScrolling() {
        val reads = AtomicInteger()
        val started = CompletableDeferred<Unit>()
        val gate = CompletableDeferred<ByteArray>().also { loading = it }
        val avatar = AuthenticatedLarkImage("hub/a:/api/v1/avatars/telegram/loaded?size=144") {
            reads.incrementAndGet()
            started.complete(Unit)
            gate.await()
        }
        var visible by mutableStateOf(true)
        var busy by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                Box(Modifier.testTag("row").width(400.dp).background(PersonaTheme.palette.background)) {
                    if (visible) InboxItemRow(record(avatar), repository, navigator, ReplyComposers(repository, rememberCoroutineScope()),
                        GroupPosition.Single, done = false, busy = busy, onlineSince = 1, onDone = {}, onHandOff = {})
                }
            }
        }
        rule.waitUntil(5_000) { started.isCompleted }
        initial().assertExists()
        val before = bounds()

        gate.complete(picture)
        rule.waitUntil(5_000) { rule.onAllNodesWithText("Z", useUnmergedTree = true).fetchSemanticsNodes().isEmpty() }
        assertEquals("loading the picture must not move or resize the row", before, bounds())
        assertEquals(1, reads.get())

        rule.runOnIdle { busy = true }
        rule.waitForIdle()
        initial().assertDoesNotExist()
        assertEquals(before, bounds())
        assertEquals("recomposing a loaded row must not read it again", 1, reads.get())

        rule.runOnIdle { visible = false }
        rule.waitForIdle()
        row().assertDoesNotExist()
        rule.runOnIdle { visible = true }
        rule.waitForIdle()
        initial().assertDoesNotExist()
        assertEquals(before, bounds())
        assertEquals("a row scrolled back must use Coil's loaded picture", 1, reads.get())
    }

    @Test fun initialStaysWithoutAPictureAndWhenItsReadFails() {
        val reads = AtomicInteger()
        val attempted = CompletableDeferred<Unit>()
        val failed = AuthenticatedLarkImage("hub/a:/api/v1/avatars/telegram/failed?size=144") {
            reads.incrementAndGet()
            attempted.complete(Unit)
            throw IOException("offline")
        }
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                Column(Modifier.width(400.dp).background(PersonaTheme.palette.background)) {
                    InboxItemRow(record(null, id = "telegram:absent", title = "Absent conversation"), repository, navigator,
                        ReplyComposers(repository, rememberCoroutineScope()), GroupPosition.Single,
                        done = false, busy = false, onlineSince = 1, onDone = {}, onHandOff = {})
                    InboxItemRow(record(failed, id = "telegram:failed", title = "Failed conversation", sender = "Yuri Example"),
                        repository, navigator, ReplyComposers(repository, rememberCoroutineScope()), GroupPosition.Single,
                        done = false, busy = false, onlineSince = 1, onDone = {}, onHandOff = {})
                }
            }
        }
        initial().assertExists()
        initial("Y").assertExists()
        val absentBefore = bounds("Absent conversation")
        val failedBefore = bounds("Failed conversation")

        rule.waitUntil(5_000) { attempted.isCompleted }
        initial().assertExists()
        initial("Y").assertExists()
        rule.onAllNodesWithText("Z", useUnmergedTree = true).assertCountEquals(1)
        rule.onAllNodesWithText("Y", useUnmergedTree = true).assertCountEquals(1)
        assertEquals(absentBefore, bounds("Absent conversation"))
        assertEquals(failedBefore, bounds("Failed conversation"))
        assertEquals(1, reads.get())
    }
}
