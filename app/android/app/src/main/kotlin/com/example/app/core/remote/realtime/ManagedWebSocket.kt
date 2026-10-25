package com.example.app.core.remote.realtime

import kotlin.coroutines.coroutineContext
import kotlin.math.pow
import kotlin.math.roundToLong
import kotlin.random.Random
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.ReceiveChannel
import kotlinx.coroutines.channels.SendChannel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString

private const val MESSAGE_BUFFER_CAPACITY = 256
private const val NORMAL_CLOSURE_CODE = 1000
private const val MAX_BACKOFF_EXPONENT = 20

/** Exponential backoff with symmetric jitter, capped at [maxDelayMillis]. */
class ReconnectBackoff(
    private val initialDelayMillis: Long = 1_000L,
    private val maxDelayMillis: Long = 30_000L,
    private val jitterRatio: Double = 0.2,
    private val random: Random = Random.Default,
) {
    init {
        require(initialDelayMillis > 0) { "initial delay must be positive" }
        require(maxDelayMillis >= initialDelayMillis) { "max delay must not be below the initial delay" }
        require(jitterRatio in 0.0..1.0) { "jitter ratio must be within [0, 1]" }
    }

    fun delayFor(attempt: Int): Long {
        require(attempt >= 1) { "attempt must start at 1" }
        val exponent = (attempt - 1).coerceAtMost(MAX_BACKOFF_EXPONENT)
        val base = (initialDelayMillis * 2.0.pow(exponent)).coerceAtMost(maxDelayMillis.toDouble())
        val jitter = base * jitterRatio * (random.nextDouble() * 2 - 1)
        return (base + jitter).roundToLong().coerceIn(0L, maxDelayMillis)
    }
}

/**
 * A websocket that stays connected: it reopens after failures and remote
 * closes with [backoff] until [stop] is called. Messages sent before the socket
 * is open, or rejected by a dying socket, are queued and flushed on the next
 * connect; [onOpen] runs after every (re)connect so callers can re-establish
 * server-side subscriptions.
 *
 * Every start owns a run-loop [Job]; state changes are only published by the
 * job currently recorded in [job], so a stopped loop's late callbacks cannot
 * clobber a restarted connection.
 */
class ManagedWebSocket(
    val url: String,
    private val opener: WebSocketOpener,
    private val scope: CoroutineScope,
    private val backoff: ReconnectBackoff = ReconnectBackoff(),
    private val delayFn: suspend (delayMillis: Long) -> Unit = { delay(it) },
    private val onOpen: (WebSocket) -> Unit = {},
) {
    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Closed)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = MESSAGE_BUFFER_CAPACITY)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    private val lock = Any()
    private val pending = ArrayDeque<String>()
    private var connected: WebSocket? = null
    private var job: Job? = null

    fun start() {
        synchronized(lock) {
            if (job?.isActive == true) return
            _state.value = ConnectionState.Connecting
            job = scope.launch { runLoop() }
        }
    }

    fun stop() {
        val running = synchronized(lock) {
            val current = job
            if (current != null) {
                job = null
                connected = null
                _state.value = ConnectionState.Closed
            }
            current
        }
        running?.cancel()
    }

    fun send(text: String) {
        synchronized(lock) {
            val open = connected
            // Preserve ordering: once anything is queued, new messages queue behind it.
            if (open == null || pending.isNotEmpty() || !open.send(text)) pending.addLast(text)
        }
    }

    private suspend fun runLoop() {
        val owner = coroutineContext[Job] ?: error("run loop requires a Job")
        var attempt = 0
        try {
            while (true) {
                val disconnect = runAttempt(owner)
                attempt = if (disconnect.wasOpened) 1 else attempt + 1
                val delayMillis = backoff.delayFor(attempt)
                publish(owner, ConnectionState.Reconnecting(attempt, delayMillis, disconnect.cause))
                delayFn(delayMillis)
            }
        } finally {
            synchronized(lock) {
                if (job === owner) {
                    job = null
                    _state.value = ConnectionState.Closed
                }
            }
        }
    }

    private suspend fun runAttempt(owner: Job): Disconnect {
        val events = Channel<SocketEvent>(Channel.UNLIMITED)
        var socket: WebSocket? = null
        try {
            val opened = opener.open(Request.Builder().url(url).build(), ChannelingListener(events))
            socket = opened
            return pumpUntilDisconnected(owner, opened, events)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return Disconnect(wasOpened = false, cause = e)
        } finally {
            // Stale listeners must not keep buffering into an orphaned channel,
            // and a socket that never finished opening still needs closing.
            events.cancel()
            if (socket != null) {
                detach(socket)
                socket.close(NORMAL_CLOSURE_CODE, null)
            }
        }
    }

    private suspend fun pumpUntilDisconnected(
        owner: Job,
        socket: WebSocket,
        events: ReceiveChannel<SocketEvent>,
    ): Disconnect {
        var wasOpened = false
        while (true) {
            val event = events.receive()
            when (event) {
                is SocketEvent.Opened -> {
                    wasOpened = true
                    attach(owner, socket)
                }
                is SocketEvent.Message -> if (isOwner(owner)) _messages.emit(event.text)
                is SocketEvent.Closing -> socket.close(event.code, event.reason)
                is SocketEvent.Closed -> return Disconnect(wasOpened, cause = null)
                is SocketEvent.Failed -> return Disconnect(wasOpened, event.cause)
            }
        }
    }

    private fun attach(owner: Job, socket: WebSocket) {
        val attached = synchronized(lock) {
            val owns = job === owner
            if (owns) {
                connected = socket
                flushPendingLocked(socket)
                _state.value = ConnectionState.Connected
            }
            owns
        }
        if (attached) onOpen(socket)
    }

    private fun flushPendingLocked(socket: WebSocket) {
        while (pending.isNotEmpty()) {
            // A rejected send means the socket is already dying; keep the
            // message for the next connection rather than dropping it.
            if (!socket.send(pending.first())) return
            pending.removeFirst()
        }
    }

    private fun detach(socket: WebSocket) {
        synchronized(lock) {
            if (connected === socket) connected = null
        }
    }

    private fun publish(owner: Job, newState: ConnectionState) {
        synchronized(lock) {
            if (job === owner) _state.value = newState
        }
    }

    private fun isOwner(owner: Job): Boolean = synchronized(lock) { job === owner }

    private class Disconnect(val wasOpened: Boolean, val cause: Throwable?)
}

private sealed interface SocketEvent {
    object Opened : SocketEvent

    class Message(val text: String) : SocketEvent

    class Closing(val code: Int, val reason: String) : SocketEvent

    class Closed(val code: Int, val reason: String) : SocketEvent

    class Failed(val cause: Throwable) : SocketEvent
}

// OkHttp delivers callbacks on its own threads; funnelling them through a
// channel lets the run loop handle them sequentially on the owning scope.
private class ChannelingListener(private val events: SendChannel<SocketEvent>) : WebSocketListener() {
    override fun onOpen(webSocket: WebSocket, response: Response) {
        events.trySend(SocketEvent.Opened)
    }

    override fun onMessage(webSocket: WebSocket, text: String) {
        events.trySend(SocketEvent.Message(text))
    }

    override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
        events.trySend(SocketEvent.Message(bytes.utf8()))
    }

    override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
        events.trySend(SocketEvent.Closing(code, reason))
    }

    override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
        events.trySend(SocketEvent.Closed(code, reason))
    }

    override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
        events.trySend(SocketEvent.Failed(t))
    }
}