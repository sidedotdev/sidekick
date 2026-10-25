package com.example.app.core.data

import com.example.app.core.remote.CompleteFlowActionRequest
import com.example.app.core.remote.Flow
import com.example.app.core.remote.FlowAction
import com.example.app.core.remote.FlowActionListResponse
import com.example.app.core.remote.FlowResponse
import com.example.app.core.remote.StubSidekickRemoteApi
import com.example.app.core.remote.Subflow
import com.example.app.core.remote.SubflowListResponse
import com.example.app.core.remote.SubflowResponse
import com.example.app.core.remote.UserResponse
import com.example.app.core.remote.realtime.ConnectionState
import com.example.app.core.remote.realtime.FakeWebSocket
import com.example.app.core.remote.realtime.FakeWebSocketOpener
import com.example.app.core.remote.realtime.RealtimeConnectionManager
import com.example.app.core.remote.realtime.ReconnectBackoff
import java.io.IOException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val BASE_URL = "http://127.0.0.1:4321/"
private const val WORKSPACE_ID = "ws-1"
private const val FLOW_ID = "flow-1"
private const val NORMAL_CLOSURE = 1000
private const val RECONNECT_DELAY_MILLIS = 1_000L
private const val T0 = "2024-01-01T00:00:00Z"
private const val T1 = "2024-01-01T00:00:01Z"
private const val T2 = "2024-01-01T00:00:02Z"
private const val T3 = "2024-01-01T00:00:03Z"

@OptIn(ExperimentalCoroutinesApi::class)
class FlowRepositoryTest {
    private val opener = FakeWebSocketOpener()
    private val api = FakeFlowApi()

    @Test
    fun `observing a flow loads the REST snapshot and subscribes to the flow, started actions and subflow chains`() =
        runTest {
            api.actions += action("a2", created = T2, status = "started", subflowId = "sf-child")
            api.actions += action("a1", created = T1, status = "complete")
            api.subflows += subflow("sf-child", parentSubflowId = "sf-parent")
            api.knownSubflows["sf-parent"] = subflow("sf-parent")

            val (state, sockets) = observeConnected(repository())

            assertEquals(listOf("a1", "a2"), state.value.actions.map { it.id })
            assertEquals("in_progress", state.value.flow?.status)
            assertEquals(setOf("sf-child", "sf-parent"), state.value.subflowsById.keys)
            assertEquals(listOf("sf-parent"), api.fetchedSubflowIds)
            assertFalse(state.value.isLoading)
            assertNull(state.value.error)
            assertEquals(ConnectionState.Connected, state.value.connection)
            assertEquals(
                setOf(FLOW_ID, "a2", "sf-child", "sf-parent").map(::subscription).toSet(),
                sockets.events.sent.toSet(),
            )
        }

    @Test
    fun `action changes are upserted by id, kept sorted by created and never replaced by older updates`() = runTest {
        api.actions += action("a1", created = T1, updated = T1, status = "started")
        val (state, sockets) = observeConnected(repository())

        sockets.actions.serverSends(encode(action("a0", created = T0, updated = T0)))
        sockets.actions.serverSends(
            encode(action("a1", created = T1, updated = T3, status = "failed", actionResult = "no credentials")),
        )
        sockets.actions.serverSends(encode(action("a1", created = T1, updated = T2, status = "complete")))
        runCurrent()

        assertEquals(listOf("a0", "a1"), state.value.actions.map { it.id })
        val failed = state.value.actions.single { it.id == "a1" }
        assertEquals("failed", failed.actionStatus)
        assertEquals("no credentials", failed.actionResult)
        assertEquals(T3, failed.updated)
    }

    @Test
    fun `action timestamps are compared as instants, not strings`() = runTest {
        api.actions += action("a1", created = "2024-01-01T00:00:01.5Z", updated = "2024-01-01T00:00:01Z", status = "started")
        api.actions += action("a2", created = "2024-01-01T00:00:01Z")
        val (state, sockets) = observeConnected(repository())
        assertEquals(listOf("a2", "a1"), state.value.actions.map { it.id })

        sockets.actions.serverSends(
            encode(action("a1", created = "2024-01-01T00:00:01.5Z", updated = "2024-01-01T00:00:01.25Z", status = "complete")),
        )
        runCurrent()
        assertEquals("complete", state.value.actions.single { it.id == "a1" }.actionStatus)

        sockets.actions.serverSends(
            encode(
                action(
                    "a1",
                    created = "2024-01-01T00:00:01.5Z",
                    updated = "2024-01-01T02:00:01.25+02:00",
                    status = "failed",
                    actionResult = "boom",
                ),
            ),
        )
        runCurrent()
        assertEquals("failed", state.value.actions.single { it.id == "a1" }.actionStatus)

        sockets.actions.serverSends(
            encode(action("a1", created = "2024-01-01T00:00:01.5Z", updated = "2024-01-01T00:00:01Z", status = "pending")),
        )
        runCurrent()
        assertEquals("failed", state.value.actions.single { it.id == "a1" }.actionStatus)
        assertEquals(listOf("a2", "a1"), state.value.actions.map { it.id })
    }

    @Test
    fun `changes between the initial snapshot and the first socket connection are picked up`() = runTest {
        api.actions += action("a1", created = T1)
        val repository = repository()
        val state = repository.observeFlow(FLOW_ID)
        runCurrent()
        assertEquals(listOf("a1"), state.value.actions.map { it.id })
        assertEquals(1, api.snapshotFetches)

        api.actions += action("a2", created = T2)
        api.flow = api.flow.copy(status = "paused")
        opener.socketsFor("action_changes_ws").single().serverOpens()
        opener.socketsFor("events").single().serverOpens()
        runCurrent()

        assertEquals(listOf("a1", "a2"), state.value.actions.map { it.id })
        assertEquals("paused", state.value.flow?.status)
        assertEquals(ConnectionState.Connected, state.value.connection)
    }

    @Test
    fun `status events received while a snapshot is in flight are not discarded by that snapshot`() = runTest {
        api.subflows += subflow("sf-1", status = "started")
        api.deferSnapshots = true
        val (state, sockets) = observeConnected(repository())
        assertTrue(state.value.isLoading)
        assertNull(state.value.flow)

        sockets.events.serverSends("""{"eventType":"status_change","parentId":"$FLOW_ID","status":"paused"}""")
        sockets.events.serverSends(
            """{"eventType":"status_change","parentId":"$FLOW_ID","targetId":"sf-1","status":"complete"}""",
        )
        runCurrent()
        // The server now reflects those events, but the in-flight response predates them.
        api.flow = api.flow.copy(status = "paused")
        api.subflows[0] = subflow("sf-1", status = "complete")
        api.releaseSnapshotResponses()
        runCurrent()

        assertEquals("paused", state.value.flow?.status)
        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)

        // The connect-triggered refresh is still held; releasing it too must not regress either status.
        api.deferSnapshots = false
        api.releaseSnapshotResponses()
        runCurrent()
        assertFalse(state.value.isLoading)
        assertEquals("paused", state.value.flow?.status)
        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)

        api.flow = api.flow.copy(status = "complete")
        api.subflows[0] = subflow("sf-1", status = "failed")
        sockets.actions.serverFails(IOException("dropped"))
        awaitReconnectAttempt(sockets).serverOpens()
        runCurrent()

        assertEquals("complete", state.value.flow?.status)
        assertEquals("failed", state.value.subflowsById.getValue("sf-1").status)
    }

    @Test
    fun `a replayed subflow status survives an older snapshot that lands afterwards`() = runTest {
        api.subflows += subflow("sf-1", status = "started")
        api.knownSubflows["sf-1"] = subflow("sf-1", status = "started")
        api.deferSnapshots = true
        val (state, sockets) = observeConnected(repository())

        sockets.events.serverSends(
            """{"eventType":"status_change","parentId":"$FLOW_ID","targetId":"sf-1","status":"complete"}""",
        )
        sockets.actions.serverSends(encode(action("a1", status = "started", subflowId = "sf-1")))
        runCurrent()
        assertTrue(state.value.subflowsById.isEmpty())

        api.releaseSubflowResponses()
        runCurrent()
        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)

        // The server now reflects the event, but the held snapshot captured "started" before it.
        api.subflows[0] = subflow("sf-1", status = "complete")
        api.releaseSnapshotResponses()
        runCurrent()
        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)

        api.deferSnapshots = false
        api.releaseSnapshotResponses()
        runCurrent()
        assertFalse(state.value.isLoading)
        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)

        api.subflows[0] = subflow("sf-1", status = "failed")
        sockets.actions.serverFails(IOException("dropped"))
        awaitReconnectAttempt(sockets).serverOpens()
        runCurrent()
        assertEquals("failed", state.value.subflowsById.getValue("sf-1").status)
    }

    @Test
    fun `status events for subflows not yet loaded apply once the subflow is fetched`() = runTest {
        val (state, sockets) = observeConnected(repository())

        sockets.events.serverSends(
            """{"eventType":"status_change","parentId":"$FLOW_ID","targetId":"sf-1","status":"complete"}""",
        )
        runCurrent()
        assertTrue(state.value.subflowsById.isEmpty())

        api.knownSubflows["sf-1"] = subflow("sf-1", status = "started")
        sockets.actions.serverSends(encode(action("a1", status = "started", subflowId = "sf-1")))
        runCurrent()

        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)
    }

    @Test
    fun `a started action from the socket subscribes to itself and its subflow chain, fetching unknown subflows once`() =
        runTest {
            val (_, sockets) = observeConnected(repository())
            api.knownSubflows["sf-child"] = subflow("sf-child", parentSubflowId = "sf-parent")
            api.knownSubflows["sf-parent"] = subflow("sf-parent")

            sockets.actions.serverSends(encode(action("a1", status = "started", subflowId = "sf-child")))
            runCurrent()

            assertEquals(listOf(FLOW_ID, "a1", "sf-child", "sf-parent").map(::subscription), sockets.events.sent)
            assertEquals(listOf("sf-child", "sf-parent"), api.fetchedSubflowIds)

            sockets.actions.serverSends(encode(action("a2", status = "complete", subflowId = "sf-child")))
            runCurrent()

            assertEquals(listOf("sf-child", "sf-parent"), api.fetchedSubflowIds)
            assertEquals(4, sockets.events.sent.size)
        }

    @Test
    fun `chat message deltas accumulate content and tool calls until the stream ends`() = runTest {
        api.actions += action("a1", status = "started")
        val (state, sockets) = observeConnected(repository())

        sockets.events.serverSends(
            """{"eventType":"chat_message_delta","flowActionId":"a1","chatMessageDelta":""" +
                """{"role":"assistant","content":"Hel","toolCalls":null,"usage":{"inputTokens":1}}}""",
        )
        sockets.events.serverSends(
            """{"eventType":"chat_message_delta","flowActionId":"a1","chatMessageDelta":""" +
                """{"content":"lo","toolCalls":[{"id":"tc1","name":"read_file","arguments":"{\"pa"}]}}""",
        )
        sockets.events.serverSends(
            """{"eventType":"chat_message_delta","flowActionId":"a1","chatMessageDelta":""" +
                """{"content":"","toolCalls":[{"id":"tc1","arguments":"th\":1}"},{"id":"tc2","name":"bash","arguments":""}]}}""",
        )
        runCurrent()
        val streaming = state.value.streaming.getValue("a1")
        assertEquals("Hello", streaming.content)
        assertEquals(
            listOf(ToolCallStream("tc1", "read_file", "{\"path\":1}"), ToolCallStream("tc2", "bash", "")),
            streaming.toolCalls,
        )
        assertFalse(streaming.finished)

        sockets.events.serverSends("""{"eventType":"end_stream","parentId":"a1"}""")
        runCurrent()
        assertTrue(state.value.streaming.getValue("a1").finished)
    }

    @Test
    fun `status changes update the flow or the targeted subflow and progress text is kept per parent`() = runTest {
        api.subflows += subflow("sf-1", status = "started")
        api.subflows += subflow("sf-2", status = "started")
        val (state, sockets) = observeConnected(repository())

        sockets.events.serverSends("""{"eventType":"status_change","parentId":"$FLOW_ID","status":"paused"}""")
        sockets.events.serverSends(
            """{"eventType":"status_change","parentId":"$FLOW_ID","targetId":"sf-1","status":"complete"}""",
        )
        sockets.events.serverSends("""{"eventType":"status_change","parentId":"sf-2","status":"failed"}""")
        sockets.events.serverSends(
            """{"eventType":"progress_text","parentId":"sf-2","text":"Running tests","details":"go test ./..."}""",
        )
        runCurrent()

        assertEquals("paused", state.value.flow?.status)
        assertEquals("complete", state.value.subflowsById.getValue("sf-1").status)
        assertEquals("failed", state.value.subflowsById.getValue("sf-2").status)
        assertEquals(
            FlowEvent.ProgressText(parentId = "sf-2", text = "Running tests", details = "go test ./..."),
            state.value.progress["sf-2"],
        )
    }

    @Test
    fun `unknown event types and malformed messages are ignored`() = runTest {
        val (state, sockets) = observeConnected(repository())

        sockets.events.serverSends("""{"eventType":"dev_run_started","parentId":"$FLOW_ID","devRunId":"run-1"}""")
        sockets.events.serverSends("not json")
        sockets.events.serverSends("""{"eventType":"chat_message_delta"}""")
        sockets.actions.serverSends("""{"unexpected":true}""")
        sockets.actions.serverSends("[]")
        sockets.actions.serverSends(encode(action("a1")))
        runCurrent()

        assertEquals(listOf("a1"), state.value.actions.map { it.id })
        assertNull(state.value.error)
        assertTrue(state.value.streaming.isEmpty())
    }

    @Test
    fun `unknown flow events decode to Unknown while keeping their payload`() {
        val json = Json { ignoreUnknownKeys = true }
        val event = json.decodeFromString(
            FlowEvent.serializer(),
            """{"eventType":"code_diff","parentId":"$FLOW_ID","diff":"+x"}""",
        )
        val unknown = event as FlowEvent.Unknown
        assertEquals("code_diff", unknown.eventType)
        assertEquals("+x", unknown.raw.getValue("diff").jsonPrimitive.content)
    }

    @Test
    fun `the REST snapshot is re-fetched and merged when the action socket reconnects`() = runTest {
        api.actions += action("a1", created = T1, updated = T1, status = "started")
        val (state, sockets) = observeConnected(repository())
        val fetchesWhileConnected = api.snapshotFetches

        api.actions[0] = action("a1", created = T1, updated = T2, status = "complete")
        api.actions += action("a2", created = T2, updated = T2, status = "started")
        api.flow = api.flow.copy(status = "complete")

        sockets.actions.serverFails(IOException("dropped"))
        runCurrent()
        assertTrue(state.value.connection is ConnectionState.Reconnecting)
        assertEquals(fetchesWhileConnected, api.snapshotFetches)

        val reopened = awaitReconnectAttempt(sockets)
        reopened.serverOpens()
        runCurrent()

        assertEquals(ConnectionState.Connected, state.value.connection)
        assertEquals(fetchesWhileConnected + 1, api.snapshotFetches)
        assertEquals(listOf("a1", "a2"), state.value.actions.map { it.id })
        assertEquals("complete", state.value.actions.first().actionStatus)
        assertEquals("complete", state.value.flow?.status)
        assertTrue(sockets.events.sent.contains(subscription("a2")))
    }

    @Test
    fun `REST failures are surfaced in the state and cleared by a later successful refresh`() = runTest {
        api.failure = IOException("offline")
        val (state, sockets) = observeConnected(repository())

        assertEquals("offline", state.value.error?.message)
        assertFalse(state.value.isLoading)
        assertTrue(state.value.actions.isEmpty())

        api.failure = null
        api.actions += action("a1")
        sockets.actions.serverFails(IOException("dropped"))
        awaitReconnectAttempt(sockets).serverOpens()
        runCurrent()

        assertNull(state.value.error)
        assertEquals(listOf("a1"), state.value.actions.map { it.id })
    }

    @Test
    fun `stopObserving releases the flow sockets and a later observe starts fresh ones`() = runTest {
        val repository = repository()
        val (state, sockets) = observeConnected(repository)

        repository.stopObserving(FLOW_ID)
        runCurrent()

        assertEquals(NORMAL_CLOSURE, sockets.actions.closeCode)
        assertEquals(NORMAL_CLOSURE, sockets.events.closeCode)
        assertEquals(ConnectionState.Closed, state.value.connection)

        val fresh = repository.observeFlow(FLOW_ID)
        runCurrent()
        assertNotSame(state, fresh)
        assertEquals(4, opener.sockets.size)
    }

    @Test
    fun `completing a flow action sends the user response and merges the returned action`() = runTest {
        api.actions += action("a1", updated = T1, status = "started")
        val repository = repository()
        val (state, _) = observeConnected(repository)
        api.completeResult = action("a1", updated = T2, status = "complete")

        val result = repository.completeFlowAction("a1", UserResponse(content = "yes", approved = true))

        assertEquals("complete", result.actionStatus)
        assertEquals(
            listOf("a1" to CompleteFlowActionRequest(UserResponse(content = "yes", approved = true))),
            api.completeRequests,
        )
        assertEquals("complete", state.value.actions.single().actionStatus)
    }

    private fun TestScope.repository(): FlowRepository {
        val realtime = RealtimeConnectionManager(
            WORKSPACE_ID,
            BASE_URL,
            opener,
            backgroundScope,
            ReconnectBackoff(jitterRatio = 0.0),
        )
        return FlowRepository(WORKSPACE_ID, api, realtime, backgroundScope)
    }

    /** Lets the backoff delay elapse and returns the socket the manager reopened for action changes. */
    private fun TestScope.awaitReconnectAttempt(previous: FlowSockets): FakeWebSocket {
        advanceTimeBy(RECONNECT_DELAY_MILLIS + 1)
        runCurrent()
        val reopened = opener.socketsFor("action_changes_ws").last()
        assertNotSame(previous.actions, reopened)
        return reopened
    }

    private fun TestScope.observeConnected(repository: FlowRepository): Pair<StateFlow<FlowState>, FlowSockets> {
        val state = repository.observeFlow(FLOW_ID)
        runCurrent()
        val sockets = FlowSockets(
            actions = opener.socketsFor("action_changes_ws").single(),
            events = opener.socketsFor("events").single(),
        )
        sockets.actions.serverOpens()
        sockets.events.serverOpens()
        runCurrent()
        return state to sockets
    }
}

private class FlowSockets(val actions: FakeWebSocket, val events: FakeWebSocket)

private class FakeFlowApi : StubSidekickRemoteApi() {
    var flow = Flow(id = FLOW_ID, workspaceId = WORKSPACE_ID, status = "in_progress", created = T0, updated = T0)
    val actions = mutableListOf<FlowAction>()
    val subflows = mutableListOf<Subflow>()
    val knownSubflows = mutableMapOf<String, Subflow>()
    val fetchedSubflowIds = mutableListOf<String>()
    val completeRequests = mutableListOf<Pair<String, CompleteFlowActionRequest>>()
    var completeResult: FlowAction? = null
    var snapshotFetches = 0
    var failure: Throwable? = null

    /** When set, snapshot and subflow responses capture server state immediately but are held until released. */
    var deferSnapshots = false
    private val heldResponses = mutableListOf<CompletableDeferred<Unit>>()
    private val heldSubflowResponses = mutableListOf<CompletableDeferred<Unit>>()

    fun releaseSnapshotResponses() {
        val held = heldResponses.toList()
        heldResponses.clear()
        held.forEach { it.complete(Unit) }
    }

    fun releaseSubflowResponses() {
        val held = heldSubflowResponses.toList()
        heldSubflowResponses.clear()
        held.forEach { it.complete(Unit) }
    }

    override suspend fun getFlow(workspaceId: String, flowId: String): FlowResponse {
        checkIds(workspaceId, flowId)
        failure?.let { throw it }
        snapshotFetches++
        return respond(FlowResponse(flow))
    }

    override suspend fun getFlowActions(workspaceId: String, flowId: String): FlowActionListResponse {
        checkIds(workspaceId, flowId)
        failure?.let { throw it }
        return respond(FlowActionListResponse(actions.toList()))
    }

    override suspend fun getFlowSubflows(workspaceId: String, flowId: String): SubflowListResponse {
        checkIds(workspaceId, flowId)
        failure?.let { throw it }
        return respond(SubflowListResponse(subflows.toList()))
    }

    private suspend fun <T> respond(response: T): T {
        if (deferSnapshots) {
            val gate = CompletableDeferred<Unit>()
            heldResponses += gate
            gate.await()
        }
        return response
    }

    override suspend fun getSubflow(workspaceId: String, subflowId: String): SubflowResponse {
        require(workspaceId == WORKSPACE_ID)
        fetchedSubflowIds += subflowId
        val response = SubflowResponse(knownSubflows[subflowId] ?: throw IOException("unknown subflow $subflowId"))
        if (deferSnapshots) {
            val gate = CompletableDeferred<Unit>()
            heldSubflowResponses += gate
            gate.await()
        }
        return response
    }

    override suspend fun completeFlowAction(
        workspaceId: String,
        flowActionId: String,
        request: CompleteFlowActionRequest,
    ): FlowAction {
        require(workspaceId == WORKSPACE_ID)
        completeRequests += flowActionId to request
        return checkNotNull(completeResult)
    }

    private fun checkIds(workspaceId: String, flowId: String) {
        require(workspaceId == WORKSPACE_ID) { "unexpected workspace $workspaceId" }
        require(flowId == FLOW_ID) { "unexpected flow $flowId" }
    }
}

private fun action(
    id: String,
    created: String = T1,
    updated: String = created,
    status: String = "complete",
    subflowId: String = "",
    actionResult: String = "",
) = FlowAction(
    id = id,
    flowId = FLOW_ID,
    workspaceId = WORKSPACE_ID,
    subflowId = subflowId,
    actionType = "test.action",
    actionStatus = status,
    actionResult = actionResult,
    created = created,
    updated = updated,
)

private fun subflow(id: String, status: String = "started", parentSubflowId: String = "") = Subflow(
    id = id,
    flowId = FLOW_ID,
    workspaceId = WORKSPACE_ID,
    name = id,
    status = status,
    parentSubflowId = parentSubflowId,
)

private fun subscription(parentId: String) = """{"parentId":"$parentId"}"""

private fun encode(action: FlowAction) = Json.encodeToString(FlowAction.serializer(), action)