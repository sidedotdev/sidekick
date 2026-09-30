package com.example.app.core.remote

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.net.InetAddress
import java.net.Socket
import java.net.URI
import com.example.app.core.remote.realtime.ConnectionState
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.isActive
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import retrofit2.HttpException

private const val TIMEOUT_MILLIS = 5_000L
private const val HEADER_TERMINATOR = "\r\n\r\n"

class SidekickRemoteSessionTest {
    private val sessions = mutableListOf<SidekickRemoteSession>()

    @After
    fun tearDown() {
        sessions.forEach(::closeQuietly)
    }

    @Test
    fun `api calls are tunnelled through the proxy with the bearer token`() = runBlocking {
        val connector = CannedHttpConnector(
            httpResponse(200, """{"workspaces":[{"id":"workspace-1","name":"Sidekick"}]}"""),
        )
        val session = createSession(connector)

        val workspaces = session.api.getWorkspaces().workspaces

        assertEquals(listOf("workspace-1"), workspaces.map(Workspace::id))
        assertEquals("ticket-1", connector.tickets.single())
        val request = connector.awaitRequest()
        assertTrue(request, request.startsWith("GET /api/v1/workspaces HTTP/1.1\r\n"))
        assertTrue(request, request.contains("\r\nAuthorization: Bearer secret-token\r\n"))
    }

    @Test
    fun `server errors through the tunnel surface as http exceptions`() = runBlocking {
        val connector = CannedHttpConnector(httpResponse(401, """{"error":"invalid device token"}"""))
        val session = createSession(connector)

        val error = assertThrows(HttpException::class.java) {
            runBlocking { session.api.getWorkspaces() }
        }

        assertEquals(401, error.code())
    }

    @Test
    fun `api is created once per session and shares the authenticated client`() {
        val session = createSession(CannedHttpConnector(httpResponse(200, "{}")))

        assertSame(session.api, session.api)
        assertTrue(InetAddress.getByName(URI(session.baseUrl).host).isLoopbackAddress)
        assertTrue(session.okHttpClient.pingIntervalMillis > 0)
    }

    @Test
    fun `close releases the proxy and the iroh connection`() {
        val connector = CannedHttpConnector(httpResponse(200, """{"workspaces":[]}"""))
        val session = createSession(connector)
        runBlocking { session.api.getWorkspaces() }

        session.close()

        assertTrue(connector.connections.single().closed)
        session.assertClosed()
    }

    @Test
    fun `workspace sessions are reused per workspace and closed with the session`() {
        val session = createSession(CannedHttpConnector(ByteArray(0)))

        val first = session.forWorkspace("ws-1")
        val second = session.forWorkspace("ws-2")

        assertSame(first, session.forWorkspace("ws-1"))
        assertNotSame(first, second)
        assertEquals("ws-1", first.workspaceId)
        assertNotSame(first.tasks, second.tasks)
        assertNotSame(first.flows, second.flows)

        session.close()

        assertThrows(IllegalStateException::class.java) { first.tasks.tasks() }
        assertThrows(IllegalStateException::class.java) { first.flows.observeFlow("flow-1") }
        assertThrows(IllegalStateException::class.java) { second.realtime.taskChanges() }
        assertThrows(IllegalStateException::class.java) { session.forWorkspace("ws-3") }
    }

    @Test
    fun `closing the session stops workspace sockets that are actively syncing`() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val session = createSession(CannedHttpConnector(httpResponse(200, """{"tasks":[]}""")), scope = scope)
        val workspace = session.forWorkspace("ws-1")
        val tasks = workspace.tasks.tasks()
        val flow = workspace.flows.observeFlow("flow-1")
        val taskSocket = workspace.realtime.taskChanges()
        val actionSocket = workspace.realtime.flowActionChanges("flow-1")
        assertTrue(taskSocket.state.value != ConnectionState.Closed)
        assertTrue(actionSocket.state.value != ConnectionState.Closed)

        session.close()

        assertEquals(ConnectionState.Closed, taskSocket.state.value)
        assertEquals(ConnectionState.Closed, actionSocket.state.value)
        assertEquals(ConnectionState.Closed, tasks.value.connection)
        assertEquals(ConnectionState.Closed, flow.value.connection)
        assertFalse(scope.isActive)
        session.assertClosed()
    }

    @Test
    fun `blank token is rejected before any proxy is started`() {
        assertThrows(IllegalArgumentException::class.java) {
            SidekickRemoteSession(PairingCredentials(ticket = "ticket-1", token = " "), CannedHttpConnector(ByteArray(0)))
        }
    }

    @Test
    fun `provider reuses the session for the same pairing and replaces it for a new one`() {
        val created = mutableListOf<SidekickRemoteSession>()
        val provider = RemoteSessionProvider { credentials ->
            createSession(CannedHttpConnector(ByteArray(0)), token = credentials.token).also { created += it }
        }
        val first = PairingCredentials(ticket = "ticket-1", token = "token-1", workspaceId = "ws-1")

        val api = provider.apiFor(first)
        assertSame(api, provider.apiFor(first.copy(workspaceId = null)))
        assertEquals(1, created.size)

        val replacement = provider.apiFor(first.copy(token = "token-2"))
        assertEquals(2, created.size)
        assertNotNull(replacement)
        assertFalse(created[0] === created[1])
        created[0].assertClosed()

        provider.close()
        created[1].assertClosed()
        assertThrows(IllegalStateException::class.java) { provider.apiFor(first) }
    }

    private fun createSession(
        connector: IrohConnector,
        token: String = "secret-token",
        scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Default),
    ): SidekickRemoteSession {
        val proxy = IrohLoopbackProxy("ticket-1", connector, Dispatchers.IO) { _, _ -> }
        return SidekickRemoteSession(token, proxy, scope = scope).also { sessions += it }
    }

    private fun SidekickRemoteSession.assertClosed() {
        val address = URI(baseUrl)
        assertThrows(IOException::class.java) {
            Socket(address.host, address.port).close()
        }
    }

    private fun httpResponse(status: Int, body: String): ByteArray {
        val payload = body.encodeToByteArray()
        val reason = if (status == 200) "OK" else "Error"
        val head = "HTTP/1.1 $status $reason\r\n" +
            "Content-Type: application/json\r\n" +
            "Content-Length: ${payload.size}\r\n" +
            "Connection: close" +
            HEADER_TERMINATOR
        return head.encodeToByteArray() + payload
    }

    /** Answers the first complete HTTP request head on every stream with the same canned response, then EOF. */
    private class CannedHttpConnector(private val response: ByteArray) : IrohConnector {
        val tickets = CopyOnWriteArrayList<String>()
        val connections = CopyOnWriteArrayList<CannedConnection>()
        private val requests = Channel<String>(Channel.UNLIMITED)

        override suspend fun connect(ticket: String): IrohConnection {
            tickets += ticket
            return CannedConnection(response, requests).also { connections += it }
        }

        suspend fun awaitRequest(): String = withTimeout(TIMEOUT_MILLIS) { requests.receive() }
    }

    private class CannedConnection(
        private val response: ByteArray,
        private val requests: Channel<String>,
    ) : IrohConnection {
        private val streams = CopyOnWriteArrayList<CannedStream>()

        @Volatile
        var closed = false

        override suspend fun openBidirectionalStream(): IrohStream =
            CannedStream(response, requests).also { streams += it }

        override fun close() {
            closed = true
            streams.forEach(CannedStream::close)
        }
    }

    private class CannedStream(
        private val response: ByteArray,
        private val requests: Channel<String>,
    ) : IrohStream {
        private val received = ByteArrayOutputStream()
        private val outgoing = Channel<ByteArray?>(Channel.UNLIMITED)
        private val responded = CompletableDeferred<Unit>()

        override suspend fun write(bytes: ByteArray) {
            if (responded.isCompleted) return
            received.write(bytes)
            val text = received.toString(Charsets.ISO_8859_1.name())
            if (text.contains(HEADER_TERMINATOR)) {
                responded.complete(Unit)
                requests.send(text.substringBefore(HEADER_TERMINATOR) + HEADER_TERMINATOR)
                outgoing.send(response)
                outgoing.send(null)
            }
        }

        override suspend fun finish() {}

        override suspend fun read(): ByteArray? = outgoing.receive()

        override fun close() {
            outgoing.close()
        }
    }
}