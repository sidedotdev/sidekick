package com.example.app.core.remote.realtime

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.WebSocket
import okhttp3.WebSocketListener

private const val NEW_MESSAGES_ONLY = "\$"

/** Seam over [OkHttpClient.newWebSocket] so sockets can be faked in tests. */
fun interface WebSocketOpener {
    fun open(request: Request, listener: WebSocketListener): WebSocket
}

fun OkHttpClient.asWebSocketOpener(): WebSocketOpener =
    WebSocketOpener { request, listener -> newWebSocket(request, listener) }

sealed interface ConnectionState {
    data object Connecting : ConnectionState

    data object Connected : ConnectionState

    data class Reconnecting(
        val attempt: Int,
        val delayMillis: Long,
        val cause: Throwable? = null,
    ) : ConnectionState

    data object Closed : ConnectionState
}

@Serializable
data class FlowEventSubscription(val parentId: String)

/**
 * Owns the realtime sockets for one workspace: a single task-changes socket
 * plus an action-changes and events socket per observed flow. Sockets are
 * started on first request and stay up until [releaseFlow] or [close].
 */
class RealtimeConnectionManager(
    private val workspaceId: String,
    baseUrl: String,
    private val opener: WebSocketOpener,
    private val scope: CoroutineScope,
    private val backoff: ReconnectBackoff = ReconnectBackoff(),
    private val delayFn: suspend (delayMillis: Long) -> Unit = { delay(it) },
    private val json: Json = Json,
) : AutoCloseable {
    private val baseUrl: HttpUrl = baseUrl.toHttpUrl()
    private val lock = Any()
    private var closed = false
    private var taskChanges: ManagedWebSocket? = null
    private val flows = LinkedHashMap<String, FlowSockets>()

    fun taskChanges(): ManagedWebSocket = synchronized(lock) {
        checkOpen()
        taskChanges ?: newSocket(
            workspaceUrl("task_changes").addQueryParameter("lastTaskStreamId", NEW_MESSAGES_ONLY).build(),
        ).also { taskChanges = it }
    }

    fun flowActionChanges(flowId: String): ManagedWebSocket = synchronized(lock) { flowSockets(flowId).actionChanges }

    fun flowEvents(flowId: String): ManagedWebSocket = synchronized(lock) { flowSockets(flowId).events }

    /**
     * Subscribes to events for [parentId] (the flow itself or one of its
     * subflows). The subscription is re-sent every time the events socket
     * reconnects, and duplicates are ignored.
     */
    fun subscribeFlowEvents(flowId: String, parentId: String) {
        val subscriptions = synchronized(lock) { flowSockets(flowId).subscriptions }
        subscriptions.subscribe(parentId)
    }

    fun releaseFlow(flowId: String) {
        synchronized(lock) { flows.remove(flowId) }?.stop()
    }

    override fun close() {
        val (tasks, flowSockets) = synchronized(lock) {
            if (closed) return
            closed = true
            val released = flows.values.toList()
            flows.clear()
            taskChanges.also { taskChanges = null } to released
        }
        tasks?.stop()
        flowSockets.forEach { it.stop() }
    }

    private fun flowSockets(flowId: String): FlowSockets {
        checkOpen()
        return flows.getOrPut(flowId) {
            val subscriptions = FlowEventSubscriptions(json)
            FlowSockets(
                actionChanges = newSocket(
                    workspaceUrl("flows/$flowId/action_changes_ws")
                        .addQueryParameter("streamMessageStartId", NEW_MESSAGES_ONLY)
                        .build(),
                ),
                events = newSocket(
                    workspaceUrl("flows/$flowId/events").build(),
                    onOpen = subscriptions::onOpen,
                ),
                subscriptions = subscriptions,
            )
        }
    }

    private fun newSocket(url: HttpUrl, onOpen: (WebSocket) -> Unit = {}): ManagedWebSocket =
        ManagedWebSocket(url.toString(), opener, scope, backoff, delayFn, onOpen).also { it.start() }

    private fun workspaceUrl(path: String): HttpUrl.Builder =
        baseUrl.newBuilder()
            .addPathSegments("ws/v1/workspaces")
            .addPathSegment(workspaceId)
            .addPathSegments(path)

    private fun checkOpen() = check(!closed) { "realtime connection manager is closed" }
}

private class FlowSockets(
    val actionChanges: ManagedWebSocket,
    val events: ManagedWebSocket,
    val subscriptions: FlowEventSubscriptions,
) {
    fun stop() {
        actionChanges.stop()
        events.stop()
    }
}

/**
 * Tracks which parent ids a flow's events socket should be subscribed to.
 * Adding a subscription and replaying them on open share one lock, so a
 * subscription added while a socket is opening is sent exactly once.
 */
private class FlowEventSubscriptions(private val json: Json) {
    private val lock = Any()
    private val parentIds = LinkedHashSet<String>()
    private var openSocket: WebSocket? = null

    fun onOpen(socket: WebSocket) = synchronized(lock) {
        openSocket = socket
        parentIds.forEach { socket.send(message(it)) }
    }

    fun subscribe(parentId: String) = synchronized(lock) {
        // Sends to a socket that has since dropped are harmless: the replay on
        // the next open covers them.
        if (parentIds.add(parentId)) openSocket?.send(message(parentId))
    }

    private fun message(parentId: String): String = json.encodeToString(FlowEventSubscription(parentId))
}