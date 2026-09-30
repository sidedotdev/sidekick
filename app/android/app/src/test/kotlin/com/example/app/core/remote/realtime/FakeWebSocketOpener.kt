package com.example.app.core.remote.realtime

import java.util.concurrent.CopyOnWriteArrayList
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString

/**
 * Hands out [FakeWebSocket]s that tests drive as if they were the server, so
 * reconnect and subscription behaviour can be verified without any network.
 */
class FakeWebSocketOpener : WebSocketOpener {
    val sockets = CopyOnWriteArrayList<FakeWebSocket>()

    /** Thrown from the next [open] call only, then cleared. */
    @Volatile
    var failNextOpen: Throwable? = null

    override fun open(request: Request, listener: WebSocketListener): WebSocket {
        failNextOpen?.let { error ->
            failNextOpen = null
            throw error
        }
        return FakeWebSocket(request, listener).also { sockets += it }
    }

    fun single(): FakeWebSocket = sockets.single()

    fun socketsFor(pathSuffix: String): List<FakeWebSocket> =
        sockets.filter { it.request.url.encodedPath.endsWith(pathSuffix) }
}

class FakeWebSocket(
    val request: Request,
    private val listener: WebSocketListener,
) : WebSocket {
    val sent = CopyOnWriteArrayList<String>()

    /** When false, [send] rejects messages the way OkHttp does for a dying socket. */
    @Volatile
    var acceptSends = true

    @Volatile
    var closeCode: Int? = null

    @Volatile
    var cancelled = false

    fun serverOpens() {
        val response = Response.Builder()
            .request(request)
            .protocol(Protocol.HTTP_1_1)
            .code(101)
            .message("Switching Protocols")
            .build()
        listener.onOpen(this, response)
    }

    fun serverSends(text: String) = listener.onMessage(this, text)

    fun serverFails(cause: Throwable) = listener.onFailure(this, cause, null)

    fun serverCloses(code: Int, reason: String) {
        listener.onClosing(this, code, reason)
        listener.onClosed(this, code, reason)
    }

    override fun request(): Request = request

    override fun queueSize(): Long = 0

    override fun send(text: String): Boolean {
        if (!acceptSends || closeCode != null || cancelled) return false
        sent += text
        return true
    }

    override fun send(bytes: ByteString): Boolean = false

    override fun close(code: Int, reason: String?): Boolean {
        if (closeCode != null) return false
        closeCode = code
        return true
    }

    override fun cancel() {
        cancelled = true
    }
}