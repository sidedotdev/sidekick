package com.example.app

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.example.app.core.data.FlowState
import com.example.app.core.data.Rfc3339Order
import com.example.app.core.data.TaskListState
import com.example.app.core.remote.CreateTaskRequest
import com.example.app.core.remote.FfiIrohConnector
import com.example.app.core.remote.FlowAction
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.SidekickRemoteSession
import com.example.app.core.remote.WorkspaceSession
import com.example.app.core.remote.realtime.ConnectionState
import java.net.HttpURLConnection
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import retrofit2.HttpException

private const val SESSION_TIMEOUT_MS = 60_000L
private const val SOCKET_TIMEOUT_MS = 60_000L
private const val API_TIMEOUT_MS = 60_000L
private const val FLOW_APPEAR_TIMEOUT_MS = 120_000L
private const val FIRST_ACTION_TIMEOUT_MS = 180_000L
private const val FAILED_ACTION_TIMEOUT_MS = 120_000L
private const val POLL_INTERVAL_MS = 500L

private const val FLOW_TYPE_BASIC_DEV = "basic_dev"
private const val ACTION_STATUS_FAILED = "failed"
private const val STATUS_CANCELED = "canceled"
private val FINISHED_TASK_STATUSES = setOf("complete", "failed", STATUS_CANCELED)

/**
 * Live on-device coverage of the realtime sync stack over iroh: websockets
 * through the loopback proxy, task and flow repositories fed by REST plus
 * sockets, and recovery after a socket drop.
 *
 * The server it runs against generally has no LLM credentials, so the task it
 * creates is expected to fail quickly; what matters is that the failure shows
 * up as ordinary synced data (a `failed` action carrying its result) rather
 * than as a client error. A server that does have credentials keeps the flow
 * running instead, so the wait for a failure is bounded and the flow is then
 * checked as a healthy in-progress one. Credentials come from the same
 * instrumentation arguments as [RemoteWorkspaceIrohInstrumentedTest],
 * optionally with `sidekickWorkspaceId` to pick the workspace the task is
 * created in.
 */
@RunWith(AndroidJUnit4::class)
class RemoteFlowSyncIrohInstrumentedTest {

    @Test
    fun flowsSyncOverIrohWebsockets() {
        val arguments = InstrumentationRegistry.getArguments()
        val ticket = arguments.getString("sidekickTicket").orEmpty().trim()
        val token = arguments.getString("sidekickToken").orEmpty().trim()
        val requestedWorkspaceId = arguments.getString("sidekickWorkspaceId").orEmpty().trim()
        assumeTrue(
            "Requires live pairing credentials; run scripts/android_phone_remote_e2e/run.sh",
            ticket.isNotEmpty() && token.isNotEmpty(),
        )

        val stages = StageLog(secrets = listOf(ticket, token))
        try {
            runBlocking {
                val session = stages.stage("session-open", SESSION_TIMEOUT_MS) {
                    SidekickRemoteSession(PairingCredentials(ticket = ticket, token = token), FfiIrohConnector())
                }
                try {
                    val workspaceId = stages.stage("resolve-workspace", API_TIMEOUT_MS) {
                        requestedWorkspaceId.ifEmpty {
                            session.api.getWorkspaces().workspaces.firstOrNull()?.id
                                ?: throw AssertionError("server has no workspaces; create one or pass sidekickWorkspaceId")
                        }
                    }
                    stages.note("workspace: $workspaceId")
                    val workspace = session.forWorkspace(workspaceId)

                    connectTaskChanges(stages, workspace)
                    rejectBogusToken(stages, ticket, workspaceId)
                    val tasks = workspace.tasks.tasks()
                    val taskId = createTask(stages, workspace, tasks)
                    try {
                        val synced = syncFlow(stages, session, workspace, tasks, taskId)
                        reconnectFlowSockets(stages, session, workspace, synced)
                    } finally {
                        // Archiving alone would leave a credentialed server
                        // running the task, so cancel first (harmless once the
                        // task has already finished or been cancelled).
                        stages.softStage("task-cleanup-cancel", API_TIMEOUT_MS) { workspace.tasks.cancelTask(taskId) }
                        stages.softStage("task-archive", API_TIMEOUT_MS) { workspace.tasks.archiveTask(taskId) }
                    }
                } finally {
                    stages.closeQuietly("session-close", session)
                }
            }
        } catch (error: Throwable) {
            // Re-thrown redacted: transport errors quote the ticket, and request
            // framing errors can quote the header line carrying the token.
            throw AssertionError(
                stages.redact("remote flow sync probe failed: ${error.describe()}\n$stages"),
            )
        } finally {
            stages.dumpSummary()
        }
    }
}

private class SyncedTask(
    val taskId: String,
    val flowId: String,
    val tasks: StateFlow<TaskListState>,
    val flowState: StateFlow<FlowState>,
)

private class HandshakeOutcome(val opened: Boolean, val statusCode: Int?, val failure: Throwable? = null) {
    override fun toString(): String =
        "opened=$opened status=${statusCode ?: "<none>"} failure=${failure?.describe() ?: "<none>"}"
}

private suspend fun connectTaskChanges(stages: StageLog, workspace: WorkspaceSession) {
    stages.stage("ws-connect", SOCKET_TIMEOUT_MS) {
        workspace.realtime.taskChanges().state.first { it == ConnectionState.Connected }
    }
}

/**
 * The bearer token is checked during the websocket handshake, so a wrong token
 * must surface as a 401 handshake failure rather than an open socket that
 * never receives anything.
 */
private suspend fun rejectBogusToken(stages: StageLog, ticket: String, workspaceId: String) {
    val bogus = stages.stage("ws-unauthorized-session", SESSION_TIMEOUT_MS) {
        SidekickRemoteSession(PairingCredentials(ticket = ticket, token = "not-a-valid-device-token"), FfiIrohConnector())
    }
    try {
        val outcome = stages.stage("ws-unauthorized", SOCKET_TIMEOUT_MS) {
            val url = bogus.baseUrl.toHttpUrl().newBuilder()
                .addPathSegments("ws/v1/workspaces")
                .addPathSegment(workspaceId)
                .addPathSegment("task_changes")
                .addQueryParameter("lastTaskStreamId", "$")
                .build()
            val handshake = CompletableDeferred<HandshakeOutcome>()
            val socket = bogus.okHttpClient.newWebSocket(
                Request.Builder().url(url).build(),
                object : WebSocketListener() {
                    override fun onOpen(webSocket: WebSocket, response: Response) {
                        handshake.complete(HandshakeOutcome(opened = true, statusCode = response.code))
                    }

                    override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                        handshake.complete(HandshakeOutcome(opened = false, statusCode = response?.code, failure = t))
                    }
                },
            )
            try {
                handshake.await()
            } finally {
                socket.cancel()
            }
        }
        stages.note("bogus token handshake: $outcome")
        assertFalse("websocket opened despite an invalid device token", outcome.opened)
        assertEquals(
            "invalid device token should be rejected with 401 during the handshake",
            HttpURLConnection.HTTP_UNAUTHORIZED,
            outcome.statusCode,
        )
    } finally {
        stages.closeQuietly("ws-unauthorized-session-close", bogus)
    }
}

private suspend fun createTask(stages: StageLog, workspace: WorkspaceSession, tasks: StateFlow<TaskListState>): String {
    stages.stage("task-list-synced", SOCKET_TIMEOUT_MS) {
        tasks.first { it.connection == ConnectionState.Connected && !it.isLoading }
    }

    val created = stages.stage("task-create", API_TIMEOUT_MS) {
        workspace.tasks.createTask(
            CreateTaskRequest(
                title = "Android e2e flow sync ${System.currentTimeMillis()}",
                description = "Created by RemoteFlowSyncIrohInstrumentedTest; safe to archive.",
                flowType = FLOW_TYPE_BASIC_DEV,
            ),
        )
    }
    stages.note("created task ${created.id} status=${created.status}")
    return created.id
}

private suspend fun syncFlow(
    stages: StageLog,
    session: SidekickRemoteSession,
    workspace: WorkspaceSession,
    tasks: StateFlow<TaskListState>,
    taskId: String,
): SyncedTask {
    stages.stage("task-appears-in-list", API_TIMEOUT_MS) {
        tasks.first { state -> state.tasks.any { it.id == taskId } }
    }

    val flowId = stages.stage("task-resolve-flow", FLOW_APPEAR_TIMEOUT_MS) {
        pollUntil { session.api.getTaskFlows(workspace.workspaceId, taskId).flows.firstOrNull()?.id }
    }
    stages.note("flow: $flowId")

    val flowState = workspace.flows.observeFlow(flowId)
    stages.stage("flow-ws-connect", SOCKET_TIMEOUT_MS) {
        flowState.first { it.connection == ConnectionState.Connected }
    }
    val withActions = stages.stage("flow-first-action", FIRST_ACTION_TIMEOUT_MS) {
        flowState.first { it.actions.isNotEmpty() && !it.isLoading }
    }
    stages.note("first snapshot: status=${withActions.flow?.status} ${withActions.actions.summary()}")
    assertNull("flow observation surfaced an error: ${withActions.error?.describe()}", withActions.error)
    assertCreatedOrdered(withActions.actions)

    // The first non-empty snapshot is typically just a `started` action, so
    // keep observing: without LLM credentials the failure arrives shortly
    // after; with credentials the flow keeps running and this wait expires.
    val withFailure = stages.stage("flow-failed-action", FAILED_ACTION_TIMEOUT_MS + SOCKET_TIMEOUT_MS) {
        withTimeoutOrNull(FAILED_ACTION_TIMEOUT_MS) {
            flowState.first { state -> state.actions.any { it.actionStatus == ACTION_STATUS_FAILED } }
        }
    }
    val observed = withFailure ?: flowState.value
    val failedActions = observed.actions.filter { it.actionStatus == ACTION_STATUS_FAILED }
    stages.note(
        if (withFailure != null) "failure observed: status=${observed.flow?.status} ${observed.actions.summary()}"
        else "no failed action within ${FAILED_ACTION_TIMEOUT_MS}ms (server has LLM credentials?): ${observed.actions.summary()}",
    )
    assertNull("flow observation surfaced an error while waiting for a failure: ${observed.error?.describe()}", observed.error)
    assertCreatedOrdered(observed.actions)
    failedActions.forEach { action ->
        assertTrue("failed action ${action.id} (${action.actionType}) lost its actionResult", action.actionResult.isNotBlank())
    }
    if (withFailure == null) {
        assertTrue(
            "flow neither failed nor stayed in progress: status=${observed.flow?.status}",
            observed.flow?.status !in FINISHED_TASK_STATUSES,
        )
    }
    return SyncedTask(taskId, flowId, tasks, flowState)
}

private fun List<FlowAction>.summary(): String =
    "actions=$size ${map { "${it.actionType}:${it.actionStatus}" }}"

/**
 * Drops the flow's sockets, changes the flow on the server while they are
 * down, then brings them back. Only a fresh REST snapshot on reconnect can
 * surface a change made while the sockets were stopped, so seeing it in the
 * state proves the re-merge rather than cached data. When the task has already
 * finished (cancellation rejected) there is nothing left to change, so the
 * proof falls back to observing the snapshot load itself.
 */
private suspend fun reconnectFlowSockets(
    stages: StageLog,
    session: SidekickRemoteSession,
    workspace: WorkspaceSession,
    synced: SyncedTask,
) {
    val flowState = synced.flowState
    val actionChanges = workspace.realtime.flowActionChanges(synced.flowId)
    val events = workspace.realtime.flowEvents(synced.flowId)

    stages.stage("ws-stop", SOCKET_TIMEOUT_MS) {
        actionChanges.stop()
        events.stop()
        actionChanges.state.first { it == ConnectionState.Closed }
        events.state.first { it == ConnectionState.Closed }
        flowState.first { it.connection == ConnectionState.Closed }
    }

    val canceled = stages.stage("task-cancel", API_TIMEOUT_MS) {
        try {
            workspace.tasks.cancelTask(synced.taskId)
            true
        } catch (error: HttpException) {
            // The server only cancels tasks that are still running; one that
            // already failed (no LLM credentials) is rejected with 400.
            if (error.code() != HttpURLConnection.HTTP_BAD_REQUEST) throw error
            stages.note("cancel rejected: ${error.response()?.errorBody()?.string()}")
            false
        }
    }

    val refreshed = stages.stage("ws-reconnect", SOCKET_TIMEOUT_MS) {
        coroutineScope {
            // Subscribed before the restart so a fast snapshot cannot finish
            // before the observer starts looking for it.
            val snapshotStarted = async(start = CoroutineStart.UNDISPATCHED) {
                flowState.first { it.connection == ConnectionState.Connected && it.isLoading }
            }
            actionChanges.start()
            events.start()
            actionChanges.state.first { it == ConnectionState.Connected }
            events.state.first { it == ConnectionState.Connected }
            snapshotStarted.await()
            flowState.first { it.connection == ConnectionState.Connected && !it.isLoading }
        }
    }
    assertNull("refresh after reconnect failed: ${refreshed.error?.describe()}", refreshed.error)
    assertCreatedOrdered(refreshed.actions)

    if (canceled) {
        val flow = stages.stage("ws-reconnect-merges-cancel", API_TIMEOUT_MS) {
            flowState.first { it.flow?.status == STATUS_CANCELED }.flow
        }
        stages.note("flow ${flow?.id} status=${flow?.status} after reconnect")
    } else {
        val serverActionIds = stages.stage("ws-reconnect-matches-rest", API_TIMEOUT_MS) {
            session.api.getFlowActions(workspace.workspaceId, synced.flowId).flowActions.map { it.id }.toSet()
        }
        assertTrue(
            "actions after reconnect ${refreshed.actions.map { it.id }} are not all known to the server $serverActionIds",
            serverActionIds.containsAll(refreshed.actions.map { it.id }),
        )
    }

    val finalTask = stages.stage("task-status-update", API_TIMEOUT_MS) {
        synced.tasks.first { state ->
            state.tasks.any { it.id == synced.taskId && it.status in FINISHED_TASK_STATUSES }
        }.tasks.first { it.id == synced.taskId }
    }
    stages.note("task ${finalTask.id} status=${finalTask.status}")
    if (canceled) assertEquals(STATUS_CANCELED, finalTask.status)
}

private fun assertCreatedOrdered(actions: List<FlowAction>) {
    val ordered = actions.sortedWith(compareBy(Rfc3339Order) { it.created })
    assertEquals("flow actions must be ordered by created", ordered.map { it.id }, actions.map { it.id })
}

private suspend fun <T : Any> pollUntil(block: suspend () -> T?): T {
    while (true) {
        block()?.let { return it }
        delay(POLL_INTERVAL_MS)
    }
}