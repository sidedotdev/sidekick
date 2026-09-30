package com.example.app.core.remote.realtime

import app.cash.turbine.test
import java.io.IOException
import kotlin.random.Random
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val BASE_URL = "http://127.0.0.1:4321/"
private const val WORKSPACE_ID = "ws-1"
private const val FLOW_ID = "flow-1"
private const val SOCKET_URL = "${BASE_URL}ws/v1/workspaces/$WORKSPACE_ID/task_changes"
private const val NORMAL_CLOSURE = 1000

@OptIn(ExperimentalCoroutinesApi::class)
class RealtimeConnectionManagerTest {
    private val opener = FakeWebSocketOpener()
    private val noJitter = ReconnectBackoff(jitterRatio = 0.0)

    @Test
    fun `messages from the server are emitted after the socket opens`() = runTest {
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter)

        socket.messages.test {
            socket.start()
            runCurrent()
            val server = opener.single()
            assertEquals(SOCKET_URL, server.request.url.toString())

            server.serverOpens()
            server.serverSends("one")
            server.serverSends("two")

            assertEquals("one", awaitItem())
            assertEquals("two", awaitItem())
            assertEquals(ConnectionState.Connected, socket.state.value)
        }
    }

    @Test
    fun `sends before open are queued and flushed once connected`() = runTest {
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter)
        socket.send("queued")
        socket.start()
        runCurrent()
        val server = opener.single()
        assertEquals(emptyList<String>(), server.sent)

        server.serverOpens()
        runCurrent()
        assertEquals(listOf("queued"), server.sent)

        socket.send("direct")
        assertEquals(listOf("queued", "direct"), server.sent)
    }

    @Test
    fun `failures reconnect with growing delays that reset after a successful open`() = runTest {
        val gate = ReconnectGate()
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter, gate::await)

        socket.state.test {
            assertEquals(ConnectionState.Closed, awaitItem())
            socket.start()
            assertEquals(ConnectionState.Connecting, awaitItem())
            runCurrent()

            opener.sockets[0].serverFails(IOException("first"))
            val first = awaitItem() as ConnectionState.Reconnecting
            assertEquals(1, first.attempt)
            assertEquals(1_000L, first.delayMillis)
            assertEquals("first", first.cause?.message)

            gate.release()
            runCurrent()
            assertEquals(2, opener.sockets.size)
            opener.sockets[1].serverFails(IOException("second"))
            val second = awaitItem() as ConnectionState.Reconnecting
            assertEquals(2, second.attempt)
            assertEquals(2_000L, second.delayMillis)

            gate.release()
            runCurrent()
            assertEquals(3, opener.sockets.size)
            opener.sockets[2].serverOpens()
            assertEquals(ConnectionState.Connected, awaitItem())

            opener.sockets[2].serverCloses(1001, "going away")
            val afterClose = awaitItem() as ConnectionState.Reconnecting
            assertEquals(1, afterClose.attempt)
            assertEquals(1_000L, afterClose.delayMillis)
            assertNull(afterClose.cause)
            runCurrent()
            assertEquals(listOf(1_000L, 2_000L, 1_000L), gate.delays)
        }
    }

    @Test
    fun `stop closes the socket and prevents reconnecting until started again`() = runTest {
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter)
        socket.start()
        runCurrent()
        val server = opener.single()
        server.serverOpens()
        runCurrent()

        socket.stop()
        runCurrent()
        assertEquals(NORMAL_CLOSURE, server.closeCode)
        assertEquals(ConnectionState.Closed, socket.state.value)

        server.serverCloses(NORMAL_CLOSURE, "")
        runCurrent()
        assertEquals(1, opener.sockets.size)

        socket.start()
        runCurrent()
        assertEquals(2, opener.sockets.size)
        assertEquals(ConnectionState.Connecting, socket.state.value)
    }

    @Test
    fun `an opener that throws is retried with backoff instead of ending the run loop`() = runTest {
        val gate = ReconnectGate()
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter, gate::await)
        opener.failNextOpen = IOException("dial failed")

        socket.start()
        runCurrent()
        val reconnecting = socket.state.value as ConnectionState.Reconnecting
        assertEquals(1, reconnecting.attempt)
        assertEquals("dial failed", reconnecting.cause?.message)
        assertEquals(0, opener.sockets.size)

        gate.release()
        runCurrent()
        opener.single().serverOpens()
        runCurrent()
        assertEquals(ConnectionState.Connected, socket.state.value)
    }

    @Test
    fun `restart during handshake releases the stale socket and ignores its callbacks`() = runTest {
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter)
        socket.start()
        runCurrent()
        val stale = opener.single()

        socket.stop()
        socket.start()
        runCurrent()
        assertEquals(2, opener.sockets.size)
        val fresh = opener.sockets[1]
        assertEquals(NORMAL_CLOSURE, stale.closeCode)
        assertNull(fresh.closeCode)
        assertEquals(ConnectionState.Connecting, socket.state.value)

        socket.messages.test {
            stale.serverOpens()
            stale.serverSends("stale")
            stale.serverCloses(NORMAL_CLOSURE, "")
            runCurrent()
            assertEquals(ConnectionState.Connecting, socket.state.value)
            assertEquals(2, opener.sockets.size)
            expectNoEvents()

            fresh.serverOpens()
            fresh.serverSends("fresh")
            assertEquals("fresh", awaitItem())
            assertEquals(ConnectionState.Connected, socket.state.value)
        }
    }

    @Test
    fun `cancelling the owning scope releases a socket that never finished opening`() = runTest {
        val scope = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        val socket = ManagedWebSocket(SOCKET_URL, opener, scope, noJitter)
        socket.start()
        runCurrent()
        val server = opener.single()
        assertNull(server.closeCode)

        scope.cancel()
        runCurrent()
        assertEquals(NORMAL_CLOSURE, server.closeCode)
        assertEquals(ConnectionState.Closed, socket.state.value)
    }

    @Test
    fun `messages rejected by a dying socket are kept for the next connection`() = runTest {
        val gate = ReconnectGate()
        val socket = ManagedWebSocket(SOCKET_URL, opener, backgroundScope, noJitter, gate::await)
        socket.send("queued")
        socket.start()
        runCurrent()
        val dying = opener.single()
        dying.acceptSends = false
        dying.serverOpens()
        runCurrent()
        assertEquals(emptyList<String>(), dying.sent)
        socket.send("direct")
        assertEquals(emptyList<String>(), dying.sent)

        dying.serverFails(IOException("gone"))
        runCurrent()
        gate.release()
        runCurrent()
        val next = opener.sockets[1]
        next.serverOpens()
        runCurrent()
        assertEquals(listOf("queued", "direct"), next.sent)
    }

    @Test
    fun `manager opens the task changes socket for the workspace`() = runTest {
        val manager = RealtimeConnectionManager(WORKSPACE_ID, BASE_URL, opener, backgroundScope, noJitter)

        manager.taskChanges()
        runCurrent()

        val url = opener.single().request.url
        assertEquals("/ws/v1/workspaces/$WORKSPACE_ID/task_changes", url.encodedPath)
        assertEquals("$", url.queryParameter("lastTaskStreamId"))
        assertTrue(manager.taskChanges() === manager.taskChanges())
    }

    @Test
    fun `flow event subscriptions are sent once and replayed after reconnect`() = runTest {
        val gate = ReconnectGate()
        val manager = RealtimeConnectionManager(WORKSPACE_ID, BASE_URL, opener, backgroundScope, noJitter, gate::await)

        manager.subscribeFlowEvents(FLOW_ID, FLOW_ID)
        runCurrent()
        val actionsUrl = opener.socketsFor("/action_changes_ws").single().request.url
        assertEquals("/ws/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID/action_changes_ws", actionsUrl.encodedPath)
        assertEquals("$", actionsUrl.queryParameter("streamMessageStartId"))
        val first = opener.socketsFor("/events").single()
        assertEquals("${BASE_URL}ws/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID/events", first.request.url.toString())
        assertEquals(emptyList<String>(), first.sent)

        first.serverOpens()
        runCurrent()
        assertEquals(listOf("""{"parentId":"flow-1"}"""), first.sent)

        manager.subscribeFlowEvents(FLOW_ID, "subflow-1")
        manager.subscribeFlowEvents(FLOW_ID, "subflow-1")
        assertEquals(listOf("""{"parentId":"flow-1"}""", """{"parentId":"subflow-1"}"""), first.sent)

        first.serverFails(IOException("dropped"))
        runCurrent()
        gate.release()
        runCurrent()
        val second = opener.socketsFor("/events").last()
        assertTrue(second !== first)
        second.serverOpens()
        runCurrent()
        assertEquals(listOf("""{"parentId":"flow-1"}""", """{"parentId":"subflow-1"}"""), second.sent)
    }

    @Test
    fun `releasing a flow closes both of its sockets for good`() = runTest {
        val manager = RealtimeConnectionManager(WORKSPACE_ID, BASE_URL, opener, backgroundScope, noJitter)
        val actions = manager.flowActionChanges(FLOW_ID)
        val events = manager.flowEvents(FLOW_ID)
        runCurrent()
        assertEquals(2, opener.sockets.size)
        opener.sockets.forEach { it.serverOpens() }
        runCurrent()

        manager.releaseFlow(FLOW_ID)
        runCurrent()
        opener.sockets.forEach { assertEquals(NORMAL_CLOSURE, it.closeCode) }
        assertEquals(ConnectionState.Closed, actions.state.value)
        assertEquals(ConnectionState.Closed, events.state.value)

        opener.sockets.toList().forEach { it.serverCloses(NORMAL_CLOSURE, "") }
        runCurrent()
        assertEquals(2, opener.sockets.size)

        manager.flowEvents(FLOW_ID)
        runCurrent()
        assertEquals(4, opener.sockets.size)
    }

    @Test
    fun `close stops every managed socket and rejects further use`() = runTest {
        val manager = RealtimeConnectionManager(WORKSPACE_ID, BASE_URL, opener, backgroundScope, noJitter)
        val tasks = manager.taskChanges()
        val events = manager.flowEvents(FLOW_ID)
        runCurrent()
        opener.sockets.forEach { it.serverOpens() }
        runCurrent()

        manager.close()
        runCurrent()
        opener.sockets.forEach { assertEquals(NORMAL_CLOSURE, it.closeCode) }
        assertEquals(ConnectionState.Closed, tasks.state.value)
        assertEquals(ConnectionState.Closed, events.state.value)
        assertTrue(runCatching { manager.taskChanges() }.exceptionOrNull() is IllegalStateException)
    }

    @Test
    fun `backoff doubles up to the cap and jitters within bounds`() {
        val exact = ReconnectBackoff(jitterRatio = 0.0)
        assertEquals(listOf(1_000L, 2_000L, 4_000L, 8_000L, 16_000L, 30_000L, 30_000L), (1..7).map(exact::delayFor))
        assertEquals(30_000L, exact.delayFor(500))

        val jittered = ReconnectBackoff(jitterRatio = 0.2, random = Random(7))
        repeat(50) {
            val delay = jittered.delayFor(1)
            assertTrue("delay $delay outside jitter bounds", delay in 800L..1_200L)
            assertTrue(jittered.delayFor(10) <= 30_000L)
        }
    }
}

/** Replaces the reconnect delay so tests decide exactly when a retry happens. */
private class ReconnectGate {
    val delays = mutableListOf<Long>()
    private val releases = Channel<Unit>(Channel.UNLIMITED)

    suspend fun await(delayMillis: Long) {
        delays += delayMillis
        releases.receive()
    }

    fun release() {
        releases.trySend(Unit)
    }
}