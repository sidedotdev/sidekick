package com.example.app.core.data

import com.example.app.core.remote.CreateTaskRequest
import com.example.app.core.remote.MessageResponse
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.Task
import com.example.app.core.remote.TaskListResponse
import com.example.app.core.remote.UpdateTaskRequest
import com.example.app.core.remote.realtime.ConnectionState
import com.example.app.core.remote.realtime.ManagedWebSocket
import com.example.app.core.remote.realtime.RealtimeConnectionManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json

data class TaskListState(
    /** Sorted by `created`; archived tasks are excluded. */
    val tasks: List<Task> = emptyList(),
    val isLoading: Boolean = false,
    val error: Throwable? = null,
    val connection: ConnectionState = ConnectionState.Closed,
)

/**
 * Keeps one workspace's task list in sync with the server: a REST snapshot for
 * the baseline, the task-changes socket for live updates and a fresh snapshot
 * on every (re)connection to cover whatever the socket missed in between.
 */
class TaskRepository(
    private val workspaceId: String,
    private val api: SidekickRemoteApi,
    private val realtime: RealtimeConnectionManager,
    private val scope: CoroutineScope,
    private val json: Json = Json { ignoreUnknownKeys = true },
) : AutoCloseable {
    private val lock = Any()
    private var closed = false
    private var sync: TaskSync? = null
    private val _state = MutableStateFlow(TaskListState())
    private val state: StateFlow<TaskListState> = _state.asStateFlow()

    // Guards the merge bookkeeping below so socket updates, mutation results
    // and REST snapshots (which may land out of order) resolve consistently.
    private val mergeLock = Any()

    /**
     * Every task seen, including archived ones. Archived copies stay as
     * tombstones so an older REST snapshot that still lists the task can't
     * resurrect it; only a genuinely newer server copy replaces them.
     */
    private val tasksById = LinkedHashMap<String, Task>()
    private var nextRequestId = 0L

    /** Ids touched by socket messages or mutations while each snapshot request was in flight. */
    private val inFlightRequests = LinkedHashMap<Long, MutableSet<String>>()

    /** Starts syncing on first call; later calls share the same state. */
    fun tasks(): StateFlow<TaskListState> = synchronized(lock) {
        check(!closed) { "task repository is closed" }
        if (sync == null) sync = TaskSync().also { it.start() }
        state
    }

    override fun close() {
        val running = synchronized(lock) {
            if (closed) return
            closed = true
            sync.also { sync = null }
        }
        running?.stop()
    }

    suspend fun createTask(request: CreateTaskRequest): Task =
        api.createTask(workspaceId, request).task.also { merge(listOf(it)) }

    suspend fun updateTask(taskId: String, request: UpdateTaskRequest): Task =
        api.updateTask(workspaceId, taskId, request).task.also { merge(listOf(it)) }

    suspend fun cancelTask(taskId: String): MessageResponse = api.cancelTask(workspaceId, taskId)

    suspend fun archiveTask(taskId: String) {
        api.archiveTask(workspaceId, taskId)
        synchronized(mergeLock) {
            tasksById[taskId]?.let { upsert(it.tombstone()) }
            publish()
        }
    }

    private fun merge(incoming: List<Task>) = synchronized(mergeLock) {
        incoming.forEach(::upsert)
        publish()
    }

    /**
     * Applies an authoritative snapshot: tasks the server no longer lists are
     * tombstoned, except those that changed while the request was in flight
     * since the snapshot may simply predate them.
     */
    private fun applySnapshot(requestId: Long, snapshot: List<Task>) = synchronized(mergeLock) {
        val touched = inFlightRequests.remove(requestId).orEmpty()
        val listed = snapshot.mapTo(HashSet()) { it.id }
        tasksById.values
            .filter { !it.isArchived && it.id !in listed && it.id !in touched }
            .forEach { upsert(it.tombstone()) }
        snapshot.forEach(::upsert)
        publish()
    }

    private fun beginRequest(): Long = synchronized(mergeLock) {
        val requestId = nextRequestId++
        inFlightRequests[requestId] = HashSet()
        requestId
    }

    private fun abandonRequest(requestId: Long) {
        synchronized(mergeLock) { inFlightRequests.remove(requestId) }
    }

    private fun upsert(task: Task) {
        inFlightRequests.values.forEach { it += task.id }
        val current = tasksById[task.id]
        if (current == null || task.supersedes(current)) tasksById[task.id] = task
    }

    private fun publish() {
        val visible = tasksById.values
            .filterNot { it.isArchived }
            .sortedWith(compareBy(Rfc3339Order) { it.created })
        _state.update { it.copy(tasks = visible) }
    }

    private inner class TaskSync {
        private val job = SupervisorJob(scope.coroutineContext[Job])
        private val syncScope = CoroutineScope(scope.coroutineContext + job)
        private val refreshMutex = Mutex()
        private val socket: ManagedWebSocket = realtime.taskChanges()

        fun start() {
            _state.update { it.copy(isLoading = true) }
            socket.start()
            syncScope.launch { socket.messages.collect(::onMessage) }
            syncScope.launch { trackConnection() }
            // Baseline shown without waiting on the websocket handshake; the
            // refresh on connect then covers anything that changed in between.
            syncScope.launch { refresh() }
        }

        fun stop() {
            job.cancel()
            socket.stop()
            _state.update { it.copy(isLoading = false, connection = ConnectionState.Closed) }
        }

        private suspend fun trackConnection() {
            socket.state.collect { current ->
                _state.update { it.copy(connection = current) }
                // The socket only streams changes published after it connects.
                if (current == ConnectionState.Connected) syncScope.launch { refresh() }
            }
        }

        private suspend fun refresh() = refreshMutex.withLock {
            _state.update { it.copy(isLoading = true) }
            val requestId = beginRequest()
            val snapshot = try {
                api.getTasks(workspaceId).tasks
            } catch (e: CancellationException) {
                abandonRequest(requestId)
                throw e
            } catch (e: Exception) {
                abandonRequest(requestId)
                _state.update { it.copy(isLoading = false, error = e) }
                return@withLock
            }
            applySnapshot(requestId, snapshot)
            _state.update { it.copy(isLoading = false, error = null) }
        }

        private fun onMessage(text: String) {
            val changes = try {
                json.decodeFromString(TaskListResponse.serializer(), text)
            } catch (e: SerializationException) {
                return
            } catch (e: IllegalArgumentException) {
                return
            }
            merge(changes.tasks)
        }
    }
}

/**
 * Whether this copy should replace [current]: later `updated` wins since socket
 * and REST deliveries may interleave out of order. On equal `updated` the
 * incoming copy wins so same-instant rewrites land, except that an archived
 * copy is never displaced by a live one, as local tombstones carry the same
 * `updated` as the snapshot they hide from.
 */
private fun Task.supersedes(current: Task): Boolean {
    val order = Rfc3339Order.compare(updated, current.updated)
    return order > 0 || (order == 0 && (isArchived || !current.isArchived))
}

/** The server omits `archived` unless set, so any value means archived. */
private val Task.isArchived: Boolean
    get() = archived != null

private fun Task.tombstone(): Task = if (isArchived) this else copy(archived = updated)