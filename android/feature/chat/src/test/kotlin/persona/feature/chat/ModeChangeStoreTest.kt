package persona.feature.chat

import java.io.IOException
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.ModeChange
import persona.core.contracts.Session
import persona.core.contracts.SessionKey

/** An Agent reads its mode history on the mode-key edges, never on a timer; a Persona conversation never reads it. */
class ModeChangeStoreTest {
    private val key = SessionKey("machine", "session")
    private val record = ModeChange(
        kind = "set", from = "default", to = "acceptEdits", applied = "now",
        actorKind = "device", deviceName = "Phone", at = "2026-11-18T09:01:00Z",
    )

    private fun session(mode: String? = "default", pending: Boolean = false) = Session(
        machineId = key.machineId, sessionId = key.sessionId, agent = "claude", status = "idle",
        permissionMode = mode, bypassPending = pending,
    )

    @Test fun opensReadOnceAndOnlyModeOrPendingRestartChangesGetTheFollowupRead() = runTest {
        val reads = mutableListOf<SessionKey>()
        val store = ModeChangeStore(backgroundScope) { session ->
            reads += session
            emptyList()
        }

        store.watch(key, session(), agentChat = true) // opened
        store.watch(key, session(), agentChat = true) // shown again: one new read, not a timer
        runCurrent()
        assertEquals(2, reads.size)
        advanceTimeBy(5_000)
        runCurrent()
        assertEquals(2, reads.size)

        store.watch(key, session(mode = "acceptEdits"), agentChat = true)
        runCurrent()
        assertEquals(3, reads.size)
        advanceTimeBy(1_999)
        runCurrent()
        assertEquals(3, reads.size)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(4, reads.size)

        store.watch(key, session(mode = "acceptEdits", pending = true), agentChat = true)
        runCurrent()
        assertEquals(5, reads.size)
        advanceTimeBy(2_000)
        runCurrent()
        assertEquals(6, reads.size)
        assertTrue(reads.all { it == key })
    }

    @Test fun personaConversationsDoNotReadOrPollTheAgentRoute() = runTest {
        var reads = 0
        val store = ModeChangeStore(backgroundScope) {
            reads++
            listOf(record)
        }

        store.watch(key, session(), agentChat = false)
        store.watch(key, session(mode = "bypassPermissions", pending = true), agentChat = false)
        advanceTimeBy(10_000)
        runCurrent()

        assertEquals(0, reads)
        assertTrue(store.changes.isEmpty())
    }

    @Test fun emptyMalformedFailedAndOlderHubResponsesLeaveWhatWasShownUnchanged() = runTest {
        var answer: suspend () -> List<ModeChange> = { listOf(record) }
        val store = ModeChangeStore(backgroundScope) { answer() }
        store.watch(key, session(), agentChat = true)
        runCurrent()
        assertEquals(listOf(record), store.changes)

        val responses = listOf<suspend () -> List<ModeChange>>(
            { emptyList() },
            { throw IllegalArgumentException("malformed response") },
            { throw IOException("offline") },
            { throw UnsupportedOperationException("older hub has no mode-changes route") },
        )
        for (response in responses) {
            answer = response
            store.watch(key, session(), agentChat = true)
            runCurrent()
            assertEquals(listOf(record), store.changes)
        }
    }
}
