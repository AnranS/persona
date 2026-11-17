package persona.core.hub

import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import persona.core.contracts.DevProbe
import persona.core.contracts.DevProbeSink
import persona.core.contracts.api.ConnectionState
import java.util.concurrent.ConcurrentLinkedQueue
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first

class DevSocketProbeTest {
    private val sink = Sink()
    @Before fun setUp() = DevProbe.install(sink)
    @After fun cleanUp() = DevProbe.install(null)

    @Test fun framesCarryOnlyTypeAndBytesAndSocketChangesBalance() = runBlocking {
        val hub = LoopbackHub()
        val config = HubConnectionConfig("test-hub", null, "token", listOf(hub.endpoint("lan")), "test")
        val connection = OkHttpHubConnection(config, HubNetwork("test"))
        try {
            connection.state.first { it == ConnectionState.Online }
            val text = """{"type":"endpoints","endpoints":[]}"""
            check(hub.socket!!.send(text))
            val arrived = await { sink.frames.any { it.type == "endpoints" } }
            assertTrue("the endpoints frame is observed", arrived)
            assertTrue(sink.frames.any { it.type == "hello" })
            assertEquals(text.toByteArray(Charsets.UTF_8).size.toLong(), sink.frames.single { it.type == "endpoints" }.bytes)
            assertEquals(1, sink.sockets.count { it == "connected" })
            assertEquals(0, sink.sockets.count { it == "dropped" })
            connection.close()
            val dropped = await { sink.sockets.contains("dropped") }
            assertTrue("closing the connection records one drop", dropped)
        } finally {
            connection.close()
            hub.close()
        }
        assertEquals(1, sink.sockets.count { it == "dropped" })
    }

    private suspend fun await(condition: () -> Boolean): Boolean = withTimeoutOrNull(5000) {
        while (!condition()) delay(10)
        true
    } == true

    private data class Frame(val type: String, val bytes: Long)
    private class Sink : DevProbeSink {
        val frames = ConcurrentLinkedQueue<Frame>()
        val sockets = ConcurrentLinkedQueue<String>()
        override fun page(name: String) = Unit
        override fun request(method: String, url: String?, status: Int?, bytesIn: Long, bytesOut: Long, millis: Long) = Unit
        override fun frame(type: String, bytes: Long) { frames += Frame(type, bytes) }
        override fun socket(event: String) { sockets += event }
    }
}
