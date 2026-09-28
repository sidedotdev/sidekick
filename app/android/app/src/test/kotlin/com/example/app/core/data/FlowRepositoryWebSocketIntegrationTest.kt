package com.example.app.core.data

import com.example.app.core.remote.SidekickRemoteApiFactory
import com.example.app.core.remote.realtime.ConnectionState
import com.example.app.core.remote.realtime.RealtimeConnectionManager
import com.example.app.core.remote.realtime.asWebSocketOpener
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.OkHttpClient
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test

private const val WORKSPACE_ID = "ws-1"
private const val FLOW_ID = "flow-1"
private const val ACTION_ID = "a1"
private const val TIMEOUT_MS = 10_000L

/**
 * Drives [FlowRepository] through real OkHttp websockets and REST against a
 * [MockWebServer], so the event path under test is the one the app ships:
 * socket frames → [RealtimeConnectionManager] → repository state.
 */
class FlowRepositoryWebSocketIntegrationTest {
    private val server = MockWebServer()
    private val events = ServerSideSocket()
    private val actionChanges = ServerSideSocket()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var realtime: RealtimeConnectionManager
    private lateinit var repository: FlowRepository

    @Before
    fun setUp() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse =
                when (request.requestUrl?.encodedPath) {
                    "/api/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID" ->
                        json("""{"flow":{"id":"$FLOW_ID","workspaceId":"$WORKSPACE_ID","status":"in_progress"}}""")
                    "/api/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID/actions" ->
                        json(
                            """{"flowActions":[{"id":"$ACTION_ID","flowId":"$FLOW_ID","actionType":"generate",""" +
                                """"actionStatus":"started","created":"2024-01-01T00:00:00Z","updated":"2024-01-01T00:00:00Z"}]}""",
                        )
                    "/api/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID/subflows" -> json("""{"subflows":[]}""")
                    "/ws/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID/action_changes_ws" ->
                        MockResponse().withWebSocketUpgrade(actionChanges)
                    "/ws/v1/workspaces/$WORKSPACE_ID/flows/$FLOW_ID/events" -> MockResponse().withWebSocketUpgrade(events)
                    else -> MockResponse().setResponseCode(404)
                }
        }
        server.start()

        val client = OkHttpClient()
        val baseUrl = server.url("/").toString()
        val api = SidekickRemoteApiFactory().create(token = "token", callFactory = client, baseUrl = baseUrl)
        realtime = RealtimeConnectionManager(WORKSPACE_ID, baseUrl, client.asWebSocketOpener(), scope)
        repository = FlowRepository(WORKSPACE_ID, api, realtime, scope)
    }

    @After
    fun tearDown() {
        repository.close()
        realtime.close()
        scope.cancel()
        server.shutdown()
    }

    @Test
    fun `unknown events on the live events socket are skipped and later events on it still apply`() = runBlocking {
        val state = repository.observeFlow(FLOW_ID)
        state.await("initial snapshot with the sockets connected") {
            it.connection == ConnectionState.Connected && !it.isLoading && it.actions.map { a -> a.id } == listOf(ACTION_ID)
        }

        val serverEvents = events.awaitOpen()
        assertEquals(
            setOf("""{"parentId":"$FLOW_ID"}""", """{"parentId":"$ACTION_ID"}"""),
            setOf(events.awaitMessage(), events.awaitMessage()),
        )

        serverEvents.send("""{"eventType":"dev_run_started","parentId":"$FLOW_ID","devRunId":"run-1"}""")
        serverEvents.send("not json")
        serverEvents.send("""{"eventType":"progress_text","parentId":"$FLOW_ID","text":"Running tests"}""")
        serverEvents.send(
            """{"eventType":"chat_message_delta","flowActionId":"$ACTION_ID","chatMessageDelta":{"role":"assistant","content":"Hello"}}""",
        )

        val updated = state.await("progress and delta applied after the unknown frames") {
            it.progress[FLOW_ID]?.text == "Running tests" && it.streaming[ACTION_ID]?.content == "Hello"
        }
        assertNull(updated.error)
        assertEquals(ConnectionState.Connected, updated.connection)
        assertEquals(listOf(ACTION_ID), updated.actions.map { it.id })
        assertEquals("started", updated.actions.single().actionStatus)
    }

    private fun json(body: String): MockResponse =
        MockResponse().setHeader("Content-Type", "application/json").setBody(body)
}

private suspend fun StateFlow<FlowState>.await(description: String, predicate: (FlowState) -> Boolean): FlowState =
    withTimeoutOrNull(TIMEOUT_MS) { first(predicate) }
        ?: throw AssertionError("timed out waiting for $description; last state: $value")

/** Server end of one websocket path, exposing the accepted socket and the frames the client sent. */
private class ServerSideSocket : WebSocketListener() {
    private val opened = CompletableDeferred<WebSocket>()
    private val received = Channel<String>(Channel.UNLIMITED)

    override fun onOpen(webSocket: WebSocket, response: Response) {
        opened.complete(webSocket)
    }

    override fun onMessage(webSocket: WebSocket, text: String) {
        received.trySend(text)
    }

    suspend fun awaitOpen(): WebSocket =
        withTimeoutOrNull(TIMEOUT_MS) { opened.await() } ?: throw AssertionError("server socket never opened")

    suspend fun awaitMessage(): String =
        withTimeoutOrNull(TIMEOUT_MS) { received.receive() } ?: throw AssertionError("client sent no message")
}