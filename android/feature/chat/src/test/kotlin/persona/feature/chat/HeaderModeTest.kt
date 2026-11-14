package persona.feature.chat

import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.decodeFromString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.designsystem.ModeTone
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.sort.canChangeModes

/**
 * The Agent header's permission mode (#3957), the rules of iOS `SessionModes` (#3719, HeaderModeTests): each mode's tone
 * and symbol, never a guessed Ask First; when the header shows it; Allow Everything offered only where the agent can take
 * it while it runs; what a switch asks first.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class HeaderModeTest {
    private fun claude(mode: String?, bypass: Boolean = false, status: String = "idle") =
        Session(machineId = "m", sessionId = "s", agent = "claude", status = status, permissionMode = mode, bypassAvailable = bypass)

    @Test fun eachModeHasItsToneAndSymbol() {
        assertEquals(ModeTone.Asks, SessionModes.headerTone("default"))
        assertEquals(ModeTone.Plans, SessionModes.headerTone("plan"))
        assertEquals(ModeTone.Acts, SessionModes.headerTone("acceptEdits"))
        assertEquals(ModeTone.Acts, SessionModes.headerTone("auto"))
        assertEquals(ModeTone.AllowsAll, SessionModes.headerTone("bypassPermissions"))
        assertEquals(PhIcons.HandPalm, SessionModes.headerIcon("default"))
        assertEquals(PhIcons.ShieldWarning, SessionModes.headerIcon("bypassPermissions"))
        // not reported yet, or a mode this app does not know: the plain symbol, never Ask First
        for (id in listOf(null, "", "dontAsk")) {
            assertEquals(ModeTone.Unknown, SessionModes.headerTone(id))
            assertEquals(PhIcons.Shield, SessionModes.headerIcon(id))
        }
    }

    @Test fun theHeaderShowsAModeToSeeOrToChange() {
        assertTrue(claude("default").canChangeModes)
        assertTrue(SessionModes.showsInHeader(claude("default")))
        // before its first prompt: nothing reported, but it can be set
        assertTrue(SessionModes.showsInHeader(claude(null)))
        // ended, with its last mode: still shown, not changeable
        assertTrue(SessionModes.showsInHeader(claude("plan", status = "offline")))
        assertFalse(SessionModes.showsInHeader(claude(null, status = "offline")))
        // Codex has none; an assistant conversation has its own bar
        assertFalse(SessionModes.showsInHeader(Session(machineId = "m", sessionId = "c", agent = "codex", status = "idle")))
        assertFalse(SessionModes.showsInHeader(claude("default").copy(assistant = true)))
    }

    @Test fun allowEverythingIsOfferedOnlyWhereTheAgentCanTakeIt() {
        assertFalse(SessionModes.switchable(claude("default")).any { it.id == SessionModes.BYPASS })
        assertEquals(SessionModes.BYPASS, SessionModes.bypassUnavailable(claude("default"))?.id)
        val can = claude("default", bypass = true)
        assertTrue(SessionModes.switchable(can).any { it.id == SessionModes.BYPASS })
        assertNull(SessionModes.bypassUnavailable(can))
        // already in it: listed as the current one, not greyed again
        assertNull(SessionModes.bypassUnavailable(claude("bypassPermissions")))
        // not running: nothing to offer
        assertNull(SessionModes.bypassUnavailable(claude("default", status = "offline")))
    }

    @Test fun switchingAsksFirstIntoAllowEverythingAndOutOfItWithNoWayBack() {
        assertEquals(SessionModes.Confirmation.AllowEverything, SessionModes.confirmation(claude("default", bypass = true), "bypassPermissions"))
        assertEquals(SessionModes.Confirmation.LeaveAllowEverything, SessionModes.confirmation(claude("bypassPermissions"), "default"))
        // it can come back: no question
        assertNull(SessionModes.confirmation(claude("bypassPermissions", bypass = true), "default"))
        assertNull(SessionModes.confirmation(claude("default"), "plan"))
        assertNull(SessionModes.confirmation(claude("plan"), "plan"))
        // an assistant conversation switches without asking
        assertNull(SessionModes.confirmation(claude("default").copy(assistant = true), "bypassPermissions"))
    }

    @Test fun bypassAvailableIsReadOffTheSession() {
        val json = """{"machine_id":"m","session_id":"s","permission_mode":"bypassPermissions","bypass_available":true}"""
        assertTrue(JsonCodec.decodeFromString<Session>(json).bypassAvailable)
        assertFalse(JsonCodec.decodeFromString<Session>("""{"machine_id":"m","session_id":"s"}""").bypassAvailable)
    }

    /** Full access by a restart (#6513, the web's `bypassRestarts` from #6476): Claude Code in a pane without a bypass flag. */
    private fun paned(mode: String? = "default", bypass: Boolean = false) = claude(mode, bypass).copy(paneId = "w1:p1")

    @Test fun allowEverythingIsOfferedByARestartForClaudeInAPane() {
        val s = paned()
        assertTrue(SessionModes.bypassRestarts(s))
        val row = SessionModes.switchable(s).single { it.id == SessionModes.BYPASS }
        assertEquals(R.string.chat_modes_bypass_restart, row.detailRes)
        assertNull(SessionModes.bypassUnavailable(s))
        // all five modes, Plan included
        assertEquals(listOf("default", "acceptEdits", "plan", "auto", SessionModes.BYPASS), SessionModes.switchable(s).map { it.id })
        // it still asks first, the dialog carrying the note
        assertEquals(SessionModes.Confirmation.AllowEverything, SessionModes.confirmation(s, SessionModes.BYPASS))
        assertEquals(R.string.chat_modes_bypass_restart, SessionModes.bypassNote(s))
    }

    @Test fun noRestartWhereItIsNotNeededOrTheNodeCannot() {
        // the process can take it: at once, no note
        assertFalse(SessionModes.bypassRestarts(paned(bypass = true)))
        assertNull(SessionModes.bypassNote(paned(bypass = true)))
        // already in it
        assertFalse(SessionModes.bypassRestarts(paned("bypassPermissions")))
        // no pane (a headless agent), a source, Codex, Antigravity, a Persona conversation
        assertFalse(SessionModes.bypassRestarts(claude("default")))
        assertFalse(SessionModes.bypassRestarts(paned().copy(sourceKind = "codex_app")))
        assertFalse(SessionModes.bypassRestarts(paned().copy(agent = "codex")))
        assertFalse(SessionModes.bypassRestarts(paned().copy(agent = "agy")))
        assertFalse(SessionModes.bypassRestarts(paned().copy(assistant = true)))
        // a node that says no terminal, or no modes
        assertFalse(SessionModes.bypassRestarts(paned().copy(capabilities = persona.core.contracts.SessionCapabilities(send = true, terminal = false))))
        assertFalse(SessionModes.bypassRestarts(paned().copy(capabilities = persona.core.contracts.SessionCapabilities(send = true, terminal = true, modes = false))))
        // disabled with its reason where it can't
        assertEquals(SessionModes.BYPASS, SessionModes.bypassUnavailable(claude("default"))?.id)
    }

    @Test fun aPendingRestartKeepsTheModeItRunsIn() {
        val s = paned("acceptEdits").copy(bypassPending = true)
        // the header and the chip draw the mode it runs in until the restart
        assertEquals(ModeTone.Acts, SessionModes.headerTone(s.permissionMode))
        assertEquals(R.string.chat_modes_bypass_pending, SessionModes.bypassNote(s))
        val row = SessionModes.switchable(s).single { it.id == SessionModes.BYPASS }
        assertEquals(R.string.chat_modes_bypass_pending, row.detailRes)
        val res = androidx.test.core.app.ApplicationProvider.getApplicationContext<android.content.Context>().resources
        assertEquals(res.getString(R.string.chat_modes_bypass_pending), SessionModes.pendingNote(s, res))
        assertNull(SessionModes.pendingNote(paned("acceptEdits"), res))
        // the hint above the composer says it, after the mode
        assertTrue(chatModeHint(s, res)!!.endsWith(res.getString(R.string.chat_modes_bypass_pending)))
    }

    @Test fun aPersonaConversationTakesItFromTheNextMessage() {
        val s = claude("default").copy(assistant = true)
        assertEquals(R.string.chat_modes_bypass_next_message, SessionModes.bypassNote(s))
        assertEquals(R.string.chat_modes_bypass_next_message, SessionModes.switchable(s).single { it.id == SessionModes.BYPASS }.detailRes)
        assertNull(SessionModes.bypassNote(claude("bypassPermissions").copy(assistant = true)))
    }

    @Test fun bypassPendingIsReadOffTheSession() {
        val json = """{"machine_id":"m","session_id":"s","permission_mode":"auto","bypass_pending":true}"""
        assertTrue(JsonCodec.decodeFromString<Session>(json).bypassPending)
        assertFalse(JsonCodec.decodeFromString<Session>("""{"machine_id":"m","session_id":"s"}""").bypassPending)
        assertFalse(JsonCodec.decodeFromString<Session>("""{"machine_id":"m","session_id":"s","bypass_pending":null}""").bypassPending)
    }
}
