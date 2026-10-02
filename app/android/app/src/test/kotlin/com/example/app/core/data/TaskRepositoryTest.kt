package com.example.app.core.data

import com.example.app.core.remote.CreateTaskRequest
import com.example.app.core.remote.MessageResponse
import com.example.app.core.remote.StubSidekickRemoteApi
import com.example.app.core.remote.Task
import com.example.app.core.remote.TaskListResponse
import com.example.app.core.remote.TaskResponse
import com.example.app.core.remote.UpdateTaskRequest
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
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

private const val BASE_URL = "http://127.0.0.1:4321/"
private const val WORKSPACE_ID = "ws-1"
private const val NORMAL_CLOSURE = 1000
private const val RECONNECT_DELAY_MILLIS = 1_000L
private const val T0 = "2024-01-01T00:00:00Z"
private const val T1 = "2024-01-01T00:00:01Z"
private const val T2 = "2024-01-01T00:00:02Z"
private const val T3 = "2024-01-01T00:00:03Z"

@OptIn(ExperimentalCoroutinesApi::class)
class TaskRepositoryTest {
    private val opener = FakeWebSocketOpener()
    private val api = FakeTaskApi()

    @Test
    fun `observing tasks loads the REST snapshot before the socket connects and refreshes once it does`() = runTest {
        api.tasks += task("t2", created = T2)
        api.tasks += task("t1", created = T1)
        val repository = repository()

        val state = repository.tasks()
        runCurrent()

        assertEquals(listOf("t1", "t2"), state.value.tasks.map { it.id })
        assertFalse(state.value.isLoading)
        assertNull(state.value.error)
        assertEquals(ConnectionState.Connecting, state.value.connection)
        assertEquals(1, api.fetches)
        assertTrue(opener.single().request.url.encodedPath.endsWith("/task_changes"))

        // A task changed after the snapshot but before the socket connected is
        // only visible through the refresh triggered by connecting.
        api.tasks += task("t3", created = T3)
        opener.single().serverOpens()
        runCurrent()

        assertEquals(ConnectionState.Connected, state.value.connection)
        assertEquals(listOf("t1", "t2", "t3"), state.value.tasks.map { it.id })
        assertEquals(2, api.fetches)
        assertSame(state, repository.tasks())
        assertEquals(1, opener.sockets.size)
    }

    @Test
    fun `task changes are upserted by id, kept sorted by created and never replaced by older updates`() = runTest {
        api.tasks += task("t1", created = T1, updated = T1, status = "todo")
        val (state, socket) = observeConnected(repository())

        socket.serverSends(changes(task("t0", created = T0)))
        socket.serverSends(changes(task("t1", created = T1, updated = T3, status = "complete")))
        socket.serverSends(changes(task("t1", created = T1, updated = T2, status = "in_progress")))
        runCurrent()

        assertEquals(listOf("t0", "t1"), state.value.tasks.map { it.id })
        val updated = state.value.tasks.single { it.id == "t1" }
        assertEquals("complete", updated.status)
        assertEquals(T3, updated.updated)
    }

    @Test
    fun `archived tasks are dropped from the list`() = runTest {
        api.tasks += task("t1", created = T1)
        api.tasks += task("t2", created = T2)
        val (state, socket) = observeConnected(repository())

        socket.serverSends(changes(task("t1", created = T1, updated = T3, archived = T3)))
        runCurrent()

        assertEquals(listOf("t2"), state.value.tasks.map { it.id })
    }

    @Test
    fun `a socket update is not overwritten by an older snapshot that lands afterwards`() = runTest {
        api.tasks += task("t1", created = T1, updated = T1, status = "todo")
        api.deferSnapshots = true
        val (state, socket) = observeConnected(repository())
        assertTrue(state.value.isLoading)

        socket.serverSends(changes(task("t1", created = T1, updated = T2, status = "in_progress")))
        runCurrent()
        // Both the initial and the on-connect snapshots predate the socket update.
        api.deferSnapshots = false
        api.releaseSnapshots()
        runCurrent()

        assertFalse(state.value.isLoading)
        assertEquals(2, api.fetches)
        assertEquals("in_progress", state.value.tasks.single().status)
    }

    @Test
    fun `an archived task is not resurrected by an older snapshot and reappears once unarchived`() = runTest {
        api.tasks += task("t1", created = T1, updated = T1)
        val (state, socket) = observeConnected(repository())
        api.deferSnapshots = true
        val reopened = reconnect(socket)

        reopened.serverSends(changes(task("t1", created = T1, updated = T2, archived = T2)))
        runCurrent()
        assertTrue(state.value.tasks.isEmpty())

        api.releaseSnapshots()
        runCurrent()
        assertTrue(state.value.tasks.isEmpty())
        assertFalse(state.value.isLoading)

        reopened.serverSends(changes(task("t1", created = T1, updated = T3)))
        runCurrent()
        assertEquals(listOf("t1"), state.value.tasks.map { it.id })
    }

    @Test
    fun `a locally archived task stays hidden when a snapshot taken before archiving lands afterwards`() = runTest {
        api.tasks += task("t1", created = T1, updated = T1)
        api.tasks += task("t2", created = T2, updated = T2)
        val repository = repository()
        val (state, socket) = observeConnected(repository)
        api.deferSnapshots = true
        reconnect(socket)

        repository.archiveTask("t1")
        runCurrent()
        assertEquals(listOf("t2"), state.value.tasks.map { it.id })

        api.releaseSnapshots()
        runCurrent()
        assertEquals(listOf("t2"), state.value.tasks.map { it.id })
    }

    @Test
    fun `tasks missing from a reconnect snapshot are dropped unless they arrived while it was in flight`() = runTest {
        api.tasks += task("t1", created = T1)
        api.tasks += task("t2", created = T2)
        val (state, socket) = observeConnected(repository())
        assertEquals(listOf("t1", "t2"), state.value.tasks.map { it.id })

        // t2 was archived while disconnected, so the server no longer lists it.
        api.tasks.removeAll { it.id == "t2" }
        api.deferSnapshots = true
        val reopened = reconnect(socket)
        reopened.serverSends(changes(task("t3", created = T3)))
        runCurrent()

        api.releaseSnapshots()
        runCurrent()

        assertEquals(listOf("t1", "t3"), state.value.tasks.map { it.id })
        assertFalse(state.value.isLoading)
    }

    @Test
    fun `the snapshot is re-fetched and merged when the socket reconnects`() = runTest {
        api.tasks += task("t1", created = T1)
        val (state, socket) = observeConnected(repository())
        assertEquals(2, api.fetches)

        socket.serverFails(IOException("gone"))
        runCurrent()
        assertTrue(state.value.connection is ConnectionState.Reconnecting)

        api.tasks += task("t2", created = T2)
        advanceTimeBy(RECONNECT_DELAY_MILLIS + 1)
        runCurrent()
        val reopened = opener.sockets.last()
        assertNotSame(socket, reopened)
        reopened.serverOpens()
        runCurrent()

        assertEquals(ConnectionState.Connected, state.value.connection)
        assertEquals(listOf("t1", "t2"), state.value.tasks.map { it.id })
        assertEquals(3, api.fetches)
    }

    @Test
    fun `REST failures are surfaced in the state and cleared by a later successful refresh`() = runTest {
        val failure = IOException("offline")
        api.failure = failure
        val repository = repository()

        val state = repository.tasks()
        runCurrent()

        assertSame(failure, state.value.error)
        assertFalse(state.value.isLoading)
        assertTrue(state.value.tasks.isEmpty())

        api.failure = null
        api.tasks += task("t1", created = T1)
        opener.single().serverOpens()
        runCurrent()

        assertNull(state.value.error)
        assertEquals(listOf("t1"), state.value.tasks.map { it.id })
    }

    @Test
    fun `malformed messages are ignored`() = runTest {
        api.tasks += task("t1", created = T1)
        val (state, socket) = observeConnected(repository())

        socket.serverSends("not json")
        socket.serverSends("""{"tasks":[{"id":"t2"}]}""")
        socket.serverSends("""{"lastTaskStreamId":"1-0"}""")
        runCurrent()

        assertEquals(listOf("t1"), state.value.tasks.map { it.id })
    }

    @Test
    fun `task mutations pass through to the api and merge their results`() = runTest {
        api.tasks += task("t1", created = T1, updated = T1, status = "todo")
        api.tasks += task("t2", created = T2)
        val repository = repository()
        val (state, _) = observeConnected(repository)

        api.createResult = task("t3", created = T3)
        val created = repository.createTask(CreateTaskRequest(title = "Task t3", flowType = "basic_dev"))
        assertEquals("t3", created.id)
        assertEquals(listOf(CreateTaskRequest(title = "Task t3", flowType = "basic_dev")), api.createRequests)

        api.updateResult = task("t1", created = T1, updated = T2, status = "in_progress")
        val updated = repository.updateTask("t1", UpdateTaskRequest(status = "in_progress"))
        assertEquals("in_progress", updated.status)
        assertEquals(listOf("t1" to UpdateTaskRequest(status = "in_progress")), api.updateRequests)

        repository.cancelTask("t3")
        repository.archiveTask("t2")

        assertEquals(listOf("t3"), api.cancelledIds)
        assertEquals(listOf("t2"), api.archivedIds)
        assertEquals(listOf("t1", "t3"), state.value.tasks.map { it.id })
        assertEquals("in_progress", state.value.tasks.first().status)
    }

    @Test
    fun `close stops the socket and rejects further observation`() = runTest {
        api.tasks += task("t1", created = T1)
        val repository = repository()
        val (state, socket) = observeConnected(repository)

        repository.close()
        runCurrent()

        assertEquals(NORMAL_CLOSURE, socket.closeCode)
        assertEquals(ConnectionState.Closed, state.value.connection)
        assertThrows(IllegalStateException::class.java) { repository.tasks() }
    }

    private fun TestScope.repository(): TaskRepository {
        val realtime = RealtimeConnectionManager(
            WORKSPACE_ID,
            BASE_URL,
            opener,
            backgroundScope,
            ReconnectBackoff(jitterRatio = 0.0),
        )
        return TaskRepository(WORKSPACE_ID, api, realtime, backgroundScope)
    }

    /** Drops [socket], lets the backoff elapse and opens the replacement, which triggers a refresh. */
    private fun TestScope.reconnect(socket: FakeWebSocket): FakeWebSocket {
        socket.serverFails(IOException("gone"))
        advanceTimeBy(RECONNECT_DELAY_MILLIS + 1)
        runCurrent()
        val reopened = opener.sockets.last()
        assertNotSame(socket, reopened)
        reopened.serverOpens()
        runCurrent()
        return reopened
    }

    private fun TestScope.observeConnected(repository: TaskRepository): Pair<StateFlow<TaskListState>, FakeWebSocket> {
        val state = repository.tasks()
        runCurrent()
        val socket = opener.single()
        socket.serverOpens()
        runCurrent()
        return state to socket
    }
}

private class FakeTaskApi : StubSidekickRemoteApi() {
    val tasks = mutableListOf<Task>()
    val createRequests = mutableListOf<CreateTaskRequest>()
    val updateRequests = mutableListOf<Pair<String, UpdateTaskRequest>>()
    val cancelledIds = mutableListOf<String>()
    val archivedIds = mutableListOf<String>()
    var createResult: Task? = null
    var updateResult: Task? = null
    var fetches = 0
    var failure: Throwable? = null

    /** When set, snapshots capture server state immediately but are held until released. */
    var deferSnapshots = false
    private val heldResponses = mutableListOf<CompletableDeferred<Unit>>()

    override suspend fun getTasks(workspaceId: String): TaskListResponse {
        assertEquals(WORKSPACE_ID, workspaceId)
        fetches++
        failure?.let { throw it }
        val snapshot = tasks.toList()
        if (deferSnapshots) {
            val gate = CompletableDeferred<Unit>()
            heldResponses += gate
            gate.await()
        }
        return TaskListResponse(snapshot)
    }

    fun releaseSnapshots() {
        heldResponses.toList().forEach { it.complete(Unit) }
        heldResponses.clear()
    }

    override suspend fun createTask(workspaceId: String, request: CreateTaskRequest): TaskResponse {
        assertEquals(WORKSPACE_ID, workspaceId)
        createRequests += request
        return TaskResponse(checkNotNull(createResult))
    }

    override suspend fun updateTask(workspaceId: String, taskId: String, request: UpdateTaskRequest): TaskResponse {
        assertEquals(WORKSPACE_ID, workspaceId)
        updateRequests += taskId to request
        return TaskResponse(checkNotNull(updateResult))
    }

    override suspend fun cancelTask(workspaceId: String, taskId: String): MessageResponse {
        assertEquals(WORKSPACE_ID, workspaceId)
        cancelledIds += taskId
        return MessageResponse("cancelled")
    }

    override suspend fun archiveTask(workspaceId: String, taskId: String) {
        assertEquals(WORKSPACE_ID, workspaceId)
        archivedIds += taskId
    }
}

private fun task(
    id: String,
    created: String,
    updated: String = created,
    status: String = "todo",
    archived: String? = null,
): Task = Task(
    id = id,
    workspaceId = WORKSPACE_ID,
    title = "Task $id",
    status = status,
    archived = archived,
    created = created,
    updated = updated,
)

/** The `{"tasks":[...],"lastTaskStreamId":...}` envelope the task changes socket sends. */
private fun changes(vararg tasks: Task): String {
    val encoded = tasks.joinToString(",") { Json.encodeToString(Task.serializer(), it) }
    return """{"tasks":[$encoded],"lastTaskStreamId":"1-0"}"""
}