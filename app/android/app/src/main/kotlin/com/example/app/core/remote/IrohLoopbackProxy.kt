package com.example.app.core.remote

import android.util.Log
import java.io.IOException
import java.net.Inet6Address
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

private const val TAG = "IrohLoopbackProxy"
private const val TCP_READ_BUFFER_SIZE = 16 * 1024
private const val ACCEPT_BACKLOG = 50

/**
 * Loopback TCP listener that tunnels every accepted connection through its own
 * iroh bidirectional stream on a single shared iroh connection. This lets stock
 * HTTP and WebSocket clients reach the remote sidekick server unchanged.
 */
class IrohLoopbackProxy(
    private val ticket: String,
    private val connector: IrohConnector,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    /** Receives a one-line timing/byte summary each time a proxied connection ends. */
    private val onStreamFinished: (summary: String) -> Unit = { summary -> Log.d(TAG, summary) },
    private val onConnectionError: (message: String, error: Throwable) -> Unit = { message, error ->
        Log.w(TAG, message, error)
    },
) : AutoCloseable {
    private val scope = CoroutineScope(SupervisorJob() + ioDispatcher)
    private val connectionMutex = Mutex()
    private val activeSockets: MutableSet<Socket> = ConcurrentHashMap.newKeySet()
    private val activeTraces = ConcurrentHashMap<Socket, StreamTrace>()
    private val streamSequence = AtomicInteger()

    // Guards publication of every owned resource against close(), so a resource
    // is either visible to close() or rejected by its creator, never leaked.
    private val lifecycleLock = Any()

    @Volatile
    private var closed = false

    @Volatile
    private var connection: IrohConnection? = null

    @Volatile
    private var serverSocket: ServerSocket? = null

    val baseUrl: String
        get() {
            val server = checkNotNull(serverSocket) { "proxy has not been started" }
            val address = server.inetAddress
            val host = if (address is Inet6Address) "[${address.hostAddress}]" else address.hostAddress
            return "http://$host:${server.localPort}/"
        }

    /**
     * Snapshot of the listener and tunnel state for attaching to connection
     * failures, so a client-side connect timeout can be told apart from a
     * closed listener, an accept loop that stopped, or a missing iroh connection.
     */
    fun describeState(): String {
        val server = serverSocket
        val listener = when {
            server == null -> "not started"
            server.isClosed -> "closed"
            else -> "listening on ${server.inetAddress.hostAddress}:${server.localPort}"
        }
        val paths = connection?.let { current ->
            try {
                current.pathSummary()
            } catch (error: Exception) {
                "unavailable (${error.javaClass.simpleName})"
            }
        } ?: "none"
        return "listener=$listener closed=$closed activeSockets=${activeSockets.size} " +
            "streamsStarted=${streamSequence.get()} irohConnection=$paths " +
            "activeStreams=${activeTraces.values.joinToString(separator = "; ")}"
    }

    fun start() {
        val server = synchronized(lifecycleLock) {
            check(!closed) { "proxy is closed" }
            check(serverSocket == null) { "proxy already started" }
            ServerSocket(0, ACCEPT_BACKLOG, InetAddress.getLoopbackAddress()).also { serverSocket = it }
        }
        scope.launch { acceptLoop(server) }
    }

    override fun close() {
        val stale = synchronized(lifecycleLock) {
            if (closed) return
            closed = true
            connection.also { connection = null }
        }
        closeQuietly(serverSocket)
        activeSockets.toList().forEach(::closeQuietly)
        scope.cancel()
        closeQuietly(stale)
    }

    private fun acceptLoop(server: ServerSocket) {
        while (!closed) {
            val socket = try {
                server.accept()
            } catch (error: IOException) {
                if (closed || server.isClosed) return
                onConnectionError("accept failed", error)
                continue
            }
            if (!register(socket)) {
                closeQuietly(socket)
                return
            }
            scope.launch { proxyConnection(socket) }
        }
    }

    private fun register(socket: Socket): Boolean = synchronized(lifecycleLock) {
        if (closed) return false
        activeSockets += socket
        true
    }

    private fun publish(fresh: IrohConnection): Boolean = synchronized(lifecycleLock) {
        if (closed) return false
        connection = fresh
        true
    }

    private suspend fun proxyConnection(socket: Socket) {
        var stream: IrohStream? = null
        val trace = StreamTrace(streamSequence.incrementAndGet())
        activeTraces[socket] = trace
        try {
            socket.tcpNoDelay = true
            val opened = openStream()
            trace.markOpened()
            stream = opened
            coroutineScope {
                launch { pumpTcpToStream(socket, opened, trace) }
                launch { pumpStreamToTcp(opened, socket, trace) }
            }
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            if (!closed) onConnectionError("proxied connection failed ($trace)", error)
        } finally {
            closeQuietly(stream)
            closeQuietly(socket)
            activeSockets -= socket
            activeTraces -= socket
            if (!closed) reportStreamFinished(trace)
        }
    }

    private fun reportStreamFinished(trace: StreamTrace) {
        val paths = try {
            connection?.pathSummary() ?: "none"
        } catch (error: Exception) {
            "unavailable (${error.javaClass.simpleName})"
        }
        try {
            onStreamFinished("stream finished $trace paths=$paths")
        } catch (error: Exception) {
            onConnectionError("stream diagnostics failed", error)
        }
    }

    private suspend fun pumpTcpToStream(socket: Socket, stream: IrohStream, trace: StreamTrace) {
        closingBothOnFailure(socket, stream) {
            val input = socket.getInputStream()
            val buffer = ByteArray(TCP_READ_BUFFER_SIZE)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                if (count > 0) {
                    stream.write(buffer.copyOf(count))
                    trace.upBytes.addAndGet(count.toLong())
                }
            }
            stream.finish()
        }
    }

    private suspend fun pumpStreamToTcp(stream: IrohStream, socket: Socket, trace: StreamTrace) {
        closingBothOnFailure(socket, stream) {
            val output = socket.getOutputStream()
            while (true) {
                val chunk = stream.read() ?: break
                trace.recordDown(chunk.size)
                output.write(chunk)
                output.flush()
                trace.forwardedBytes.addAndGet(chunk.size.toLong())
            }
            trace.markEof()
            shutdownOutputQuietly(socket)
        }
    }

    // Blocking socket reads ignore coroutine cancellation, so a failing pump has
    // to close both endpoints itself to unblock its sibling.
    private inline fun closingBothOnFailure(socket: Socket, stream: IrohStream, block: () -> Unit) {
        try {
            block()
        } catch (error: Throwable) {
            closeQuietly(stream)
            closeQuietly(socket)
            throw error
        }
    }

    private fun shutdownOutputQuietly(socket: Socket) {
        try {
            if (!socket.isClosed && !socket.isOutputShutdown) socket.shutdownOutput()
        } catch (_: IOException) {
        }
    }

    private suspend fun openStream(): IrohStream {
        val current = sharedConnection()
        return try {
            current.openBidirectionalStream()
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            // Failing to open a stream almost always means the connection died,
            // so drop it and dial once more before giving up on this socket.
            discardConnection(current)
            sharedConnection().openBidirectionalStream()
        }
    }

    private suspend fun sharedConnection(): IrohConnection = connectionMutex.withLock {
        connection ?: connector.connect(ticket).also { fresh ->
            if (!publish(fresh)) {
                closeQuietly(fresh)
                throw IOException("proxy closed while connecting")
            }
        }
    }

    private suspend fun discardConnection(stale: IrohConnection) {
        connectionMutex.withLock {
            synchronized(lifecycleLock) {
                if (connection === stale) connection = null
            }
        }
        closeQuietly(stale)
    }
}

/**
 * Byte and timing accounting for one proxied stream, so a slow or truncated
 * transfer can be told apart from a slow server or a client-side timeout.
 */
private class StreamTrace(private val id: Int) {
    private val startedAtNanos = System.nanoTime()
    val upBytes = AtomicLong()
    val forwardedBytes = AtomicLong()
    private val downBytes = AtomicLong()
    private val downChunks = AtomicLong()
    private val lastDownAtNanos = AtomicLong(startedAtNanos)

    @Volatile
    private var openedAtNanos = 0L

    @Volatile
    private var firstDownAtNanos = 0L

    @Volatile
    private var eofAtNanos = 0L

    fun markOpened() {
        openedAtNanos = System.nanoTime()
    }

    fun markEof() {
        eofAtNanos = System.nanoTime()
    }

    fun recordDown(count: Int) {
        val now = System.nanoTime()
        if (downChunks.getAndIncrement() == 0L) firstDownAtNanos = now
        downBytes.addAndGet(count.toLong())
        lastDownAtNanos.set(now)
    }

    override fun toString(): String {
        fun sinceStart(atNanos: Long) = if (atNanos == 0L) "-" else "${(atNanos - startedAtNanos) / 1_000_000}ms"
        return "stream#$id up=${upBytes.get()}B down=${downBytes.get()}B/${downChunks.get()}chunks " +
            "forwarded=${forwardedBytes.get()}B " +
            "open@${sinceStart(openedAtNanos)} firstDown@${sinceStart(firstDownAtNanos)} " +
            "lastDown@${sinceStart(lastDownAtNanos.get())} eof@${sinceStart(eofAtNanos)} " +
            "age=${(System.nanoTime() - startedAtNanos) / 1_000_000}ms"
    }
}
