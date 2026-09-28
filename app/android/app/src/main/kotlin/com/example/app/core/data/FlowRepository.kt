package com.example.app.core.data

import com.example.app.core.remote.CompleteFlowActionRequest
import com.example.app.core.remote.Flow
import com.example.app.core.remote.FlowAction
import com.example.app.core.remote.MessageResponse
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.Subflow
import com.example.app.core.remote.UserActionRequest
import com.example.app.core.remote.UserResponse
import com.example.app.core.remote.realtime.ConnectionState
import com.example.app.core.remote.realtime.RealtimeConnectionManager
import java.util.concurrent.ConcurrentHashMap
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json

private const val ACTION_STATUS_STARTED = "started"

data class ToolCallStream(
    val id: String,
    val name: String = "",
    val arguments: String = "",
)

/** LLM output streamed for one flow action while it is in progress. */
data class StreamingData(
    val content: String = "",
    val toolCalls: List<ToolCallStream> = emptyList(),
    val finished: Boolean = false,
)

data class FlowState(
    val flowId: String,
    val flow: Flow? = null,
    /** Sorted by `created`. */
    val actions: List<FlowAction> = emptyList(),
    val subflowsById: Map<String, Subflow> = emptyMap(),
    /** Keyed by flow action id. */
    val streaming: Map<String, StreamingData> = emptyMap(),
    /** Latest progress text keyed by its parent (flow, subflow or action id). */
    val progress: Map<String, FlowEvent.ProgressText> = emptyMap(),
    val isLoading: Boolean = false,
    val error: Throwable? = null,
    val connection: ConnectionState = ConnectionState.Closed,
)

/**
 * Keeps a local, observable copy of flows in one workspace in sync with the
 * server: REST snapshots establish the baseline and the realtime sockets keep
 * it current, with a fresh snapshot merged in after every reconnect.
 */
class FlowRepository(
    private val workspaceId: String,
    private val api: SidekickRemoteApi,
    private val realtime: RealtimeConnectionManager,
    private val scope: CoroutineScope,
    private val json: Json = Json { ignoreUnknownKeys = true },
) : AutoCloseable {
    private val lock = Any()
    private var closed = false
    private val observations = LinkedHashMap<String, FlowObservation>()

    fun observeFlow(flowId: String): StateFlow<FlowState> = synchronized(lock) {
        check(!closed) { "flow repository is closed" }
        observations.getOrPut(flowId) { FlowObservation(flowId).also { it.start() } }.state
    }

    fun stopObserving(flowId: String) {
        synchronized(lock) { observations.remove(flowId) }?.stop()
    }

    override fun close() {
        val released = synchronized(lock) {
            if (closed) return
            closed = true
            observations.values.toList().also { observations.clear() }
        }
        released.forEach { it.stop() }
    }

    suspend fun completeFlowAction(flowActionId: String, response: UserResponse): FlowAction {
        val action = api.completeFlowAction(workspaceId, flowActionId, CompleteFlowActionRequest(response))
        synchronized(lock) { observations[action.flowId] }?.mergeActions(listOf(action))
        return action
    }

    suspend fun sendUserAction(flowId: String, actionType: String): MessageResponse =
        api.sendUserAction(workspaceId, flowId, UserActionRequest(actionType))

    suspend fun pauseFlow(flowId: String): MessageResponse = api.pauseFlow(workspaceId, flowId)

    suspend fun cancelFlow(flowId: String): MessageResponse = api.cancelFlow(workspaceId, flowId)

    private inner class FlowObservation(private val flowId: String) {
        private val _state = MutableStateFlow(FlowState(flowId = flowId, isLoading = true))
        val state: StateFlow<FlowState> = _state.asStateFlow()

        private val job = SupervisorJob(scope.coroutineContext[Job])
        private val observationScope = CoroutineScope(scope.coroutineContext + job)
        private val refreshMutex = Mutex()
        private val trackedSubflowIds: MutableSet<String> = ConcurrentHashMap.newKeySet()

        // Serializes status changes from events against REST merges so a
        // response that predates an event can never silently discard it.
        private val statusLock = Any()
        private var nextRequestId = 0L
        private val inFlightRequests = LinkedHashSet<Long>()

        /**
         * Realtime statuses (keyed by flow or subflow id) that REST may not
         * reflect yet. Each remembers the requests in flight when it arrived and
         * is replayed over those responses, and over whichever response first
         * loads its entity, then dropped so later fresh snapshots win again.
         */
        private val pendingStatuses = LinkedHashMap<String, PendingStatus>()

        fun start() {
            val actionChanges = realtime.flowActionChanges(flowId)
            val events = realtime.flowEvents(flowId)
            realtime.subscribeFlowEvents(flowId, flowId)
            observationScope.launch { actionChanges.messages.collect(::onActionMessage) }
            observationScope.launch { events.messages.collect(::onEventMessage) }
            observationScope.launch { trackConnection(actionChanges.state) }
            // Baseline shown without waiting on the websocket handshake; the
            // refresh on connect then covers anything that changed in between.
            observationScope.launch { refresh() }
        }

        fun stop() {
            job.cancel()
            realtime.releaseFlow(flowId)
            _state.update { it.copy(isLoading = false, connection = ConnectionState.Closed) }
        }

        fun mergeActions(incoming: List<FlowAction>) {
            _state.update { it.copy(actions = mergeActionsByRecency(it.actions, incoming)) }
            trackActions(incoming.map { it.id })
        }

        private suspend fun trackConnection(connection: StateFlow<ConnectionState>) {
            connection.collect { current ->
                _state.update { it.copy(connection = current) }
                // The socket only streams messages published after it connects,
                // so every (re)connection needs a snapshot to fill the gap.
                if (current == ConnectionState.Connected) observationScope.launch { refresh() }
            }
        }

        private suspend fun refresh() = refreshMutex.withLock {
            _state.update { it.copy(isLoading = true) }
            val requestId = beginRequest()
            val snapshot = try {
                coroutineScope {
                    val flow = async { api.getFlow(workspaceId, flowId).flow }
                    val actions = async { api.getFlowActions(workspaceId, flowId).flowActions }
                    val subflows = async { api.getFlowSubflows(workspaceId, flowId).subflows }
                    Snapshot(flow.await(), actions.await(), subflows.await())
                }
            } catch (e: CancellationException) {
                abandonRequest(requestId)
                throw e
            } catch (e: Exception) {
                abandonRequest(requestId)
                _state.update { it.copy(isLoading = false, error = e) }
                return@withLock
            }
            mergeSnapshot(requestId, snapshot)
            trackActions(snapshot.actions.map { it.id })
        }

        private fun mergeSnapshot(requestId: Long, snapshot: Snapshot) = synchronized(statusLock) {
            inFlightRequests -= requestId
            val loadedIds = snapshot.subflows.mapTo(hashSetOf(flowId)) { it.id }
            val overrides = pendingOverrides(requestId, _state.value) { it in loadedIds }
            _state.update { current ->
                current.copy(
                    flow = snapshot.flow,
                    actions = mergeActionsByRecency(current.actions, snapshot.actions),
                    subflowsById = current.subflowsById + snapshot.subflows.associateBy { it.id },
                    isLoading = false,
                    error = null,
                ).withStatuses(overrides)
            }
            dropSettledStatuses()
        }

        private fun beginRequest(): Long = synchronized(statusLock) {
            (nextRequestId++).also { inFlightRequests += it }
        }

        /** A request that produced no data can no longer clobber anything, so stop waiting on it. */
        private fun abandonRequest(requestId: Long) = synchronized(statusLock) {
            inFlightRequests -= requestId
            pendingStatuses.values.forEach { it.awaiting -= requestId }
            dropSettledStatuses()
        }

        /**
         * Statuses to reapply over a completed request's data: those the
         * request predates, plus those whose entity this request loads first.
         * Must be called under [statusLock].
         */
        private fun pendingOverrides(requestId: Long, before: FlowState, loads: (String) -> Boolean): Map<String, String> {
            val overrides = LinkedHashMap<String, String>()
            for ((targetId, pending) in pendingStatuses) {
                val predatesEvent = pending.awaiting.remove(requestId)
                val firstLoad = loads(targetId) && !before.hasStatusTarget(targetId)
                if (predatesEvent || firstLoad) overrides[targetId] = pending.status
            }
            return overrides
        }

        /** Must be called under [statusLock]. */
        private fun dropSettledStatuses() {
            val state = _state.value
            pendingStatuses.entries.removeAll { (targetId, pending) ->
                pending.awaiting.isEmpty() && state.hasStatusTarget(targetId)
            }
        }

        /**
         * Subscribes to streaming events for in-progress actions and to the
         * status of every subflow on each action's ancestry chain, based on the
         * merged state rather than the possibly stale incoming copies.
         */
        private fun trackActions(actionIds: List<String>) {
            val actionsById = _state.value.actions.associateBy { it.id }
            for (id in actionIds) {
                val action = actionsById[id] ?: continue
                if (action.actionStatus == ACTION_STATUS_STARTED) realtime.subscribeFlowEvents(flowId, action.id)
                if (action.subflowId.isNotBlank()) trackSubflowChain(action.subflowId)
            }
        }

        private fun trackSubflowChain(subflowId: String) {
            observationScope.launch {
                var currentId = subflowId
                while (currentId.isNotBlank() && trackedSubflowIds.add(currentId)) {
                    val subflow = _state.value.subflowsById[currentId] ?: fetchSubflow(currentId)
                    if (subflow == null) {
                        // Leave the id untracked so the next action referencing it retries the fetch.
                        trackedSubflowIds.remove(currentId)
                        return@launch
                    }
                    realtime.subscribeFlowEvents(flowId, currentId)
                    currentId = subflow.parentSubflowId
                }
            }
        }

        private suspend fun fetchSubflow(subflowId: String): Subflow? {
            val requestId = beginRequest()
            val fetched = try {
                api.getSubflow(workspaceId, subflowId).subflow
            } catch (e: CancellationException) {
                abandonRequest(requestId)
                throw e
            } catch (e: Exception) {
                abandonRequest(requestId)
                return null
            }
            synchronized(statusLock) {
                inFlightRequests -= requestId
                val overrides = pendingOverrides(requestId, _state.value) { it == subflowId }
                _state.update { current ->
                    // A snapshot may have loaded this subflow meanwhile with fresher data; don't regress it.
                    val loaded = if (subflowId in current.subflowsById) {
                        current
                    } else {
                        current.copy(subflowsById = current.subflowsById + (subflowId to fetched))
                    }
                    loaded.withStatuses(overrides)
                }
                dropSettledStatuses()
            }
            return fetched
        }

        private fun onActionMessage(text: String) {
            val action = decodeOrNull(FlowAction.serializer(), text) ?: return
            mergeActions(listOf(action))
        }

        private fun onEventMessage(text: String) {
            when (val event = decodeOrNull(FlowEvent.serializer(), text) ?: return) {
                is FlowEvent.ChatMessageDelta -> _state.update { current ->
                    val existing = current.streaming[event.flowActionId] ?: StreamingData()
                    current.copy(
                        streaming = current.streaming + (event.flowActionId to existing.applying(event.chatMessageDelta)),
                    )
                }
                is FlowEvent.StatusChange -> applyStatusChange(event)
                is FlowEvent.ProgressText -> _state.update { it.copy(progress = it.progress + (event.parentId to event)) }
                is FlowEvent.EndStream -> _state.update { current ->
                    val existing = current.streaming[event.parentId] ?: return@update current
                    current.copy(streaming = current.streaming + (event.parentId to existing.copy(finished = true)))
                }
                is FlowEvent.Unknown -> Unit
            }
        }

        private fun applyStatusChange(event: FlowEvent.StatusChange) {
            val targetId = if (event.parentId == flowId && event.targetId.isBlank()) {
                flowId
            } else {
                event.targetId.ifBlank { event.parentId }
            }
            synchronized(statusLock) {
                var applied = false
                _state.update { current ->
                    current.withStatus(targetId, event.status)?.also { applied = true } ?: current
                }
                if (applied && inFlightRequests.isEmpty()) {
                    pendingStatuses.remove(targetId)
                } else {
                    pendingStatuses[targetId] = PendingStatus(event.status, inFlightRequests.toMutableSet())
                }
            }
        }

        private fun FlowState.hasStatusTarget(targetId: String): Boolean =
            if (targetId == flowId) flow != null else targetId in subflowsById

        /** Null when [targetId] is neither this flow nor a loaded subflow. */
        private fun FlowState.withStatus(targetId: String, status: String): FlowState? =
            if (targetId == flowId) {
                flow?.let { copy(flow = it.copy(status = status)) }
            } else {
                subflowsById[targetId]?.let { copy(subflowsById = subflowsById + (targetId to it.copy(status = status))) }
            }

        private fun FlowState.withStatuses(statuses: Map<String, String>): FlowState =
            statuses.entries.fold(this) { state, (targetId, status) -> state.withStatus(targetId, status) ?: state }

        private fun <T> decodeOrNull(deserializer: kotlinx.serialization.DeserializationStrategy<T>, text: String): T? =
            try {
                json.decodeFromString(deserializer, text)
            } catch (e: SerializationException) {
                null
            } catch (e: IllegalArgumentException) {
                null
            }
    }
}

private class Snapshot(
    val flow: Flow,
    val actions: List<FlowAction>,
    val subflows: List<Subflow>,
)

private class PendingStatus(val status: String, val awaiting: MutableSet<Long>)

/**
 * Upserts [incoming] into [existing] by id, keeping whichever copy has the
 * later `updated` since socket and REST deliveries may interleave out of order.
 * Equal `updated` favours the incoming copy so a same-instant rewrite lands.
 */
private fun mergeActionsByRecency(existing: List<FlowAction>, incoming: List<FlowAction>): List<FlowAction> {
    val byId = LinkedHashMap<String, FlowAction>(existing.size + incoming.size)
    existing.forEach { byId[it.id] = it }
    incoming.forEach { action ->
        val current = byId[action.id]
        if (current == null || Rfc3339Order.compare(action.updated, current.updated) >= 0) byId[action.id] = action
    }
    return byId.values.sortedWith(compareBy(Rfc3339Order) { it.created })
}

private fun StreamingData.applying(delta: ChatMessageDeltaPayload): StreamingData {
    val merged = toolCalls.toMutableList()
    delta.toolCalls.forEach { incoming ->
        val index = merged.indexOfFirst { it.id == incoming.id }
        if (index == -1) {
            merged += ToolCallStream(incoming.id, incoming.name, incoming.arguments)
        } else {
            val existing = merged[index]
            merged[index] = existing.copy(
                name = incoming.name.ifBlank { existing.name },
                arguments = existing.arguments + incoming.arguments,
            )
        }
    }
    return copy(content = content + delta.content, toolCalls = merged)
}