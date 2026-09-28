package com.example.app.core.remote

import java.io.EOFException
import java.io.IOException
import java.net.Socket
import java.net.SocketException
import java.net.URI
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

private const val TIMEOUT_MILLIS = 5_000L

class IrohLoopbackProxyTest {
    private val proxies = mutableListOf<IrohLoopbackProxy>()
    private val sockets = mutableListOf<Socket>()
    private val reportedErrors = CopyOnWriteArrayList<Pair<String, Throwable>>()

    @After
    fun tearDown() {
        sockets.forEach(::closeQuietly)
        proxies.forEach(::closeQuietly)
    }

    @Test
    fun `bytes are forwarded in both directions`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val socket = connect(proxy)

        socket.getOutputStream().apply {
            write("hello".encodeToByteArray())
            flush()
        }
        val stream = connector.awaitConnection().awaitStream()
        assertArrayEquals("hello".encodeToByteArray(), stream.awaitWritten(5))

        stream.respond("world".encodeToByteArray())
        assertArrayEquals("world".encodeToByteArray(), socket.readExactly(5))
        assertEquals("ticket", connector.tickets.single())
    }

    @Test
    fun `tcp output shutdown finishes the iroh stream`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val socket = connect(proxy)
        socket.getOutputStream().apply {
            write("x".encodeToByteArray())
            flush()
        }
        val stream = connector.awaitConnection().awaitStream()
        stream.awaitWritten(1)

        socket.shutdownOutput()

        withTimeout(TIMEOUT_MILLIS) { stream.finished.await() }
        // The response direction must stay usable after a half-close.
        stream.respond("late".encodeToByteArray())
        assertArrayEquals("late".encodeToByteArray(), socket.readExactly(4))
    }

    @Test
    fun `iroh stream eof closes tcp input`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val socket = connect(proxy)
        socket.getOutputStream().apply {
            write("x".encodeToByteArray())
            flush()
        }
        val stream = connector.awaitConnection().awaitStream()
        stream.awaitWritten(1)

        stream.respond("bye".encodeToByteArray())
        stream.respond(null)

        assertArrayEquals("bye".encodeToByteArray(), socket.readExactly(3))
        assertEquals(-1, socket.getInputStream().read())
    }

    @Test
    fun `concurrent connections use separate streams on one iroh connection`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val first = connect(proxy)
        val second = connect(proxy)
        first.getOutputStream().apply {
            write("a".encodeToByteArray())
            flush()
        }
        second.getOutputStream().apply {
            write("b".encodeToByteArray())
            flush()
        }

        val connection = connector.awaitConnection()
        val streamOne = connection.awaitStream()
        val streamTwo = connection.awaitStream()
        assertNotSame(streamOne, streamTwo)

        val payloads = setOf(
            streamOne.awaitWritten(1).decodeToString(),
            streamTwo.awaitWritten(1).decodeToString(),
        )
        assertEquals(setOf("a", "b"), payloads)
        assertEquals(1, connector.connections.size)
    }

    @Test
    fun `stream open failure reconnects and retries`() = runBlocking {
        val connector = FakeIrohConnector(failStreamOpensOnFirstConnection = true)
        val proxy = startProxy(connector)
        val socket = connect(proxy)
        socket.getOutputStream().apply {
            write("retry".encodeToByteArray())
            flush()
        }

        val stale = connector.awaitConnection()
        val stream = connector.awaitConnection().awaitStream()
        assertArrayEquals("retry".encodeToByteArray(), stream.awaitWritten(5))
        assertTrue(stale.closed)
        assertEquals(2, connector.connections.size)
    }

    @Test
    fun `close stops accepting and closes the iroh connection`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val socket = connect(proxy)
        socket.getOutputStream().apply {
            write("x".encodeToByteArray())
            flush()
        }
        val connection = connector.awaitConnection()
        connection.awaitStream().awaitWritten(1)
        val address = URI(proxy.baseUrl)

        proxy.close()

        assertTrue(connection.closed)
        socket.assertClosedByPeer()
        try {
            Socket(address.host, address.port).close()
            fail("expected connection refused after close")
        } catch (_: IOException) {
        }
        try {
            proxy.start()
            fail("expected start after close to be rejected")
        } catch (_: IllegalStateException) {
        }
    }

    @Test
    fun `connection dialed while closing is closed instead of leaked`() = runBlocking {
        val connectGate = CompletableDeferred<Unit>()
        val connector = FakeIrohConnector(connectGate = connectGate)
        val proxy = startProxy(connector)
        connect(proxy)
        connector.awaitConnectAttempt()

        proxy.close()
        connectGate.complete(Unit)

        val connection = connector.awaitConnection()
        awaitCondition("late connection closed") { connection.closed }
    }

    @Test
    fun `stream read failure closes both endpoints and later clients still work`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val socket = connect(proxy)
        val connection = connector.awaitConnection()
        val stream = connection.awaitStream()

        stream.failReads(IOException("stream reset"))

        socket.assertClosedByPeer()
        awaitCondition("stream closed") { stream.closed }
        awaitCondition("failure reported") { reportedErrors.any { it.first == "proxied connection failed" } }

        val next = connect(proxy)
        next.getOutputStream().apply {
            write("again".encodeToByteArray())
            flush()
        }
        assertArrayEquals("again".encodeToByteArray(), connection.awaitStream().awaitWritten(5))
    }

    @Test
    fun `stream write failure closes both endpoints and later clients still work`() = runBlocking {
        val connector = FakeIrohConnector()
        val proxy = startProxy(connector)
        val socket = connect(proxy)
        val connection = connector.awaitConnection()
        val stream = connection.awaitStream()
        stream.failWrites = true

        socket.getOutputStream().apply {
            write("doomed".encodeToByteArray())
            flush()
        }

        socket.assertClosedByPeer()
        awaitCondition("stream closed") { stream.closed }

        val next = connect(proxy)
        next.getOutputStream().apply {
            write("again".encodeToByteArray())
            flush()
        }
        assertArrayEquals("again".encodeToByteArray(), connection.awaitStream().awaitWritten(5))
    }

    private fun startProxy(connector: IrohConnector): IrohLoopbackProxy =
        IrohLoopbackProxy("ticket", connector, Dispatchers.IO) { message, error ->
            reportedErrors += message to error
        }.also {
            it.start()
            proxies += it
        }

    private suspend fun awaitCondition(description: String, condition: () -> Boolean) {
        try {
            withTimeout(TIMEOUT_MILLIS) {
                while (!condition()) delay(10)
            }
        } catch (_: kotlinx.coroutines.TimeoutCancellationException) {
            fail("timed out waiting for: $description")
        }
    }

    /** Passes only on a genuine EOF or reset; a read timeout means the socket was left hanging. */
    private fun Socket.assertClosedByPeer() {
        val result = try {
            getInputStream().read()
        } catch (_: SocketException) {
            -1
        }
        assertEquals(-1, result)
    }

    private fun connect(proxy: IrohLoopbackProxy): Socket {
        val address = URI(proxy.baseUrl)
        return Socket(address.host, address.port).also {
            it.soTimeout = TIMEOUT_MILLIS.toInt()
            sockets += it
        }
    }

    private fun Socket.readExactly(count: Int): ByteArray {
        val buffer = ByteArray(count)
        val input = getInputStream()
        var offset = 0
        while (offset < count) {
            val read = input.read(buffer, offset, count - offset)
            if (read < 0) throw EOFException("socket closed after $offset of $count bytes")
            offset += read
        }
        return buffer
    }

    private class FakeIrohConnector(
        private val failStreamOpensOnFirstConnection: Boolean = false,
        private val connectGate: CompletableDeferred<Unit>? = null,
    ) : IrohConnector {
        val connections = CopyOnWriteArrayList<FakeIrohConnection>()
        val tickets = CopyOnWriteArrayList<String>()
        private val connectAttempts = Channel<Unit>(Channel.UNLIMITED)
        private val dialed = Channel<FakeIrohConnection>(Channel.UNLIMITED)

        // A native dial in flight cannot be interrupted, so the whole dial runs
        // non-cancellably even when the proxy is closed while it is gated.
        override suspend fun connect(ticket: String): IrohConnection = withContext(NonCancellable) {
            check(connectAttempts.trySend(Unit).isSuccess)
            connectGate?.await()
            tickets += ticket
            val failOpens = failStreamOpensOnFirstConnection && connections.isEmpty()
            FakeIrohConnection(failOpens).also {
                connections += it
                check(dialed.trySend(it).isSuccess)
            }
        }

        suspend fun awaitConnectAttempt() = withTimeout(TIMEOUT_MILLIS) { connectAttempts.receive() }

        /** Waits for the next connection the proxy dials, in dial order. */
        suspend fun awaitConnection(): FakeIrohConnection = withTimeout(TIMEOUT_MILLIS) { dialed.receive() }
    }

    private class FakeIrohConnection(
        private val failStreamOpens: Boolean,
    ) : IrohConnection {
        private val streams = CopyOnWriteArrayList<FakeIrohStream>()
        private val opened = Channel<FakeIrohStream>(Channel.UNLIMITED)

        @Volatile
        var closed = false

        override suspend fun openBidirectionalStream(): IrohStream {
            if (failStreamOpens) throw IOException("connection lost")
            return FakeIrohStream().also {
                streams += it
                check(opened.trySend(it).isSuccess)
            }
        }

        suspend fun awaitStream(): FakeIrohStream = withTimeout(TIMEOUT_MILLIS) { opened.receive() }

        override fun close() {
            closed = true
            streams.forEach(FakeIrohStream::close)
        }
    }

    private class FakeIrohStream : IrohStream {
        private val written = Channel<ByteArray>(Channel.UNLIMITED)
        private val incoming = Channel<ByteArray?>(Channel.UNLIMITED)
        val finished = CompletableDeferred<Unit>()

        @Volatile
        var failWrites = false

        @Volatile
        var closed = false

        override suspend fun write(bytes: ByteArray) {
            if (failWrites) throw IOException("stream write failed")
            written.send(bytes)
        }

        /** Makes pending and future reads fail, as a reset or broken iroh stream would. */
        fun failReads(error: IOException) {
            incoming.close(error)
        }

        override suspend fun finish() {
            finished.complete(Unit)
        }

        override suspend fun read(): ByteArray? = incoming.receive()

        /** Queues bytes for the proxy to deliver to TCP; `null` signals end of stream. */
        fun respond(bytes: ByteArray?) {
            incoming.trySend(bytes)
        }

        suspend fun awaitWritten(count: Int): ByteArray = withTimeout(TIMEOUT_MILLIS) {
            var collected = byteArrayOf()
            while (collected.size < count) collected += written.receive()
            collected
        }

        override fun close() {
            closed = true
            incoming.close(IOException("stream closed"))
        }
    }
}