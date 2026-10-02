package com.example.app.core.remote

import android.util.Log
import java.io.IOException
import java.net.Inet6Address
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.ConcurrentHashMap
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
    private val onConnectionError: (message: String, error: Throwable) -> Unit = { message, error ->
        Log.w(TAG, message, error)
    },
) : AutoCloseable {
    private val scope = CoroutineScope(SupervisorJob() + ioDispatcher)
    private val connectionMutex = Mutex()
    private val activeSockets: MutableSet<Socket> = ConcurrentHashMap.newKeySet()

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
        try {
            socket.tcpNoDelay = true
            val opened = openStream()
            stream = opened
            coroutineScope {
                launch { pumpTcpToStream(socket, opened) }
                launch { pumpStreamToTcp(opened, socket) }
            }
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            if (!closed) onConnectionError("proxied connection failed", error)
        } finally {
            closeQuietly(stream)
            closeQuietly(socket)
            activeSockets -= socket
        }
    }

    private suspend fun pumpTcpToStream(socket: Socket, stream: IrohStream) {
        closingBothOnFailure(socket, stream) {
            val input = socket.getInputStream()
            val buffer = ByteArray(TCP_READ_BUFFER_SIZE)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                if (count > 0) stream.write(buffer.copyOf(count))
            }
            stream.finish()
        }
    }

    private suspend fun pumpStreamToTcp(stream: IrohStream, socket: Socket) {
        closingBothOnFailure(socket, stream) {
            val output = socket.getOutputStream()
            while (true) {
                val chunk = stream.read() ?: break
                output.write(chunk)
                output.flush()
            }
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