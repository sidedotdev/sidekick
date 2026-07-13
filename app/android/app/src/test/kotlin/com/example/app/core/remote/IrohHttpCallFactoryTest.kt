package com.example.app.core.remote

import java.io.IOException
import java.util.ArrayDeque
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class IrohHttpCallFactoryTest {
    @Test
    fun `call writes authenticated HTTP request and parses fixed response`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\n" +
                "Content-Type: application/json\r\n" +
                "Content-Length: 11\r\n" +
                "\r\n" +
                """{"ok":true}""",
        )
        val connector = FakeIrohConnector(stream)
        val request = Request.Builder()
            .url("http://sidekick/api/v1/workspaces/?status=active")
            .header("Authorization", "Bearer token")
            .build()

        IrohHttpCallFactory("ticket", connector).newCall(request).execute().use { response ->
            assertEquals(200, response.code)
            assertEquals("""{"ok":true}""", response.body?.string())
        }

        assertEquals("ticket", connector.ticket)
        val sent = stream.written.decodeToString()
        assertTrue(sent.startsWith("GET /api/v1/workspaces/?status=active HTTP/1.1\r\n"))
        assertTrue(sent.contains("Authorization: Bearer token\r\n"))
        assertTrue(sent.contains("Connection: close\r\n"))
        assertTrue(stream.finished)
        assertTrue(stream.closed)
        assertTrue(connector.connection.closed)
    }

    @Test
    fun `call decodes chunked responses`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" +
                "5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n",
        )
        val request = Request.Builder()
            .url("http://sidekick/tasks")
            .build()

        IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
            .newCall(request)
            .execute()
            .use { response ->
                assertEquals("hello world", response.body?.string())
            }
    }

    @Test
    fun `call writes request body and derives content headers`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n",
        )
        val payload = """{"name":"task"}"""
        val request = Request.Builder()
            .url("http://sidekick/tasks")
            .post(payload.toRequestBody("application/json".toMediaType()))
            .build()

        IrohHttpCallFactory("ticket", FakeIrohConnector(stream)).newCall(request).execute()
        val sent = stream.written.decodeToString()

        assertTrue(sent.startsWith("POST /tasks HTTP/1.1\r\n"))
        assertTrue(sent.contains("Content-Type: application/json"))
        assertTrue(sent.contains("Content-Length: ${payload.encodeToByteArray().size}\r\n"))
        assertTrue(sent.contains("\r\n\r\n$payload"))
    }

    @Test
    fun `call rejects truncated fixed length responses`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nabc",
        )

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
        assertTrue(stream.closed)
    }

    @Test
    fun `call rejects malformed chunked responses`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" +
                "5\r\nhello\n0\r\n\r\n",
        )

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
    }

    @Test
    fun `call rejects trailing data after chunked response`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" +
                "0\r\n\r\ntrailing",
        )

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
    }

    @Test
    fun `call rejects unsupported or invalid transfer codings`() {
        val responses = listOf(
            "gzip",
            "chunked, gzip",
            "gzip, chunked",
            "chunked, chunked",
            "chunked,",
            ",chunked",
        )

        responses.forEach { transferEncoding ->
            val stream = FakeIrohStream(
                "HTTP/1.1 200 OK\r\nTransfer-Encoding: $transferEncoding\r\n\r\n" +
                    "0\r\n\r\n",
            )

            assertFailsWithIOException {
                IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                    .newCall(Request.Builder().url("http://sidekick/tasks").build())
                    .execute()
            }
            assertTrue(stream.closed)
        }
    }

    @Test
    fun `call rejects transfer encoding combined with content length`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\n" +
                "Transfer-Encoding: chunked\r\n" +
                "Content-Length: 0\r\n\r\n" +
                "0\r\n\r\n",
        )

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
        assertTrue(stream.closed)
    }

    @Test
    fun `call preserves HTTP one point zero protocol`() {
        val stream = FakeIrohStream(
            "HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nok",
        )

        IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
            .newCall(Request.Builder().url("http://sidekick/tasks").build())
            .execute()
            .use { response ->
                assertEquals(okhttp3.Protocol.HTTP_1_0, response.protocol)
            }
    }

    @Test
    fun `call validates every content length occurrence`() {
        val responses = listOf(
            "Content-Length: 2\r\nContent-Length: 2\r\n",
            "Content-Length: 2, 2\r\n",
        )

        responses.forEach { contentLengthHeaders ->
            val stream = FakeIrohStream(
                "HTTP/1.1 200 OK\r\n$contentLengthHeaders\r\nok",
            )

            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
                .close()
        }

        listOf(
            "Content-Length: 2\r\nContent-Length: 3\r\n",
            "Content-Length: 2, 3\r\n",
            "Content-Length: nope\r\n",
            "Content-Length: 2,\r\n",
        ).forEach { contentLengthHeaders ->
            val stream = FakeIrohStream(
                "HTTP/1.1 200 OK\r\n$contentLengthHeaders\r\nok",
            )

            assertFailsWithIOException {
                IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                    .newCall(Request.Builder().url("http://sidekick/tasks").build())
                    .execute()
            }
            assertTrue(stream.closed)
        }
    }

    @Test
    fun `call rejects malformed chunk trailers`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" +
                "0\r\nMalformed-Trailer\r\n\r\n",
        )

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
        assertTrue(stream.closed)
    }

    @Test
    fun `stream close failure does not prevent connection cleanup`() {
        val stream = FakeIrohStream(
            "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n",
            throwOnClose = true,
        )
        val connector = FakeIrohConnector(stream)

        IrohHttpCallFactory("ticket", connector)
            .newCall(Request.Builder().url("http://sidekick/tasks").build())
            .execute()

        assertTrue(stream.closed)
        assertTrue(connector.connection.closed)
    }

    @Test
    fun `stream open failure closes the connection`() {
        val connection = OpenFailingIrohConnection()
        val connector = object : IrohConnector {
            override suspend fun connect(ticket: String): IrohConnection = connection
        }

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", connector)
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
        assertTrue(connection.closed)
    }

    @Test
    fun `call rejects responses over the size limit`() {
        val oversizedBody = ByteArray(16 * 1024 * 1024 + 1) { 'x'.code.toByte() }
        val response = "HTTP/1.1 200 OK\r\n\r\n".encodeToByteArray() + oversizedBody
        val stream = object : IrohStream {
            var closed = false

            override suspend fun write(bytes: ByteArray) = Unit

            override suspend fun finish() = Unit

            override suspend fun read(): ByteArray? =
                if (closed) null else response.also { closed = true }

            override fun close() {
                closed = true
            }
        }

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
    }

    @Test
    fun `call rejects an invalid status protocol`() {
        val stream = FakeIrohStream(
            "HTTP/2 200 OK\r\nContent-Length: 0\r\n\r\n",
        )

        assertFailsWithIOException {
            IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                .newCall(Request.Builder().url("http://sidekick/tasks").build())
                .execute()
        }
    }

    @Test
    fun `async call reports transport failure once`() {
        val stream = FakeIrohStream("not an HTTP response")
        val callbackCalled = CountDownLatch(1)
        var failureCount = 0
        val call = IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
            .newCall(Request.Builder().url("http://sidekick/tasks").build())

        call.enqueue(
            object : Callback {
                override fun onFailure(call: okhttp3.Call, e: IOException) {
                    failureCount += 1
                    callbackCalled.countDown()
                }

                override fun onResponse(call: okhttp3.Call, response: Response) {
                    response.close()
                    callbackCalled.countDown()
                }
            },
        )

        assertTrue(callbackCalled.await(2, TimeUnit.SECONDS))
        assertEquals(1, failureCount)
        assertTrue(stream.closed)
    }

    @Test
    fun `async callback failure does not invoke failure callback`() {
        val stream = FakeIrohStream("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
        val callbackCalled = CountDownLatch(1)
        var failureCount = 0
        val call = IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
            .newCall(Request.Builder().url("http://sidekick/tasks").build())

        call.enqueue(
            object : Callback {
                override fun onFailure(call: okhttp3.Call, e: IOException) {
                    failureCount += 1
                }

                override fun onResponse(call: okhttp3.Call, response: Response) {
                    response.close()
                    callbackCalled.countDown()
                    throw IOException("callback failed")
                }
            },
        )

        assertTrue(callbackCalled.await(2, TimeUnit.SECONDS))
        Thread.sleep(100)
        assertEquals(0, failureCount)
        assertTrue(stream.closed)
    }

    @Test
    fun `async call invokes callback and cleans up`() {
        val stream = FakeIrohStream("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
        val callbackCalled = CountDownLatch(1)
        var callbackResponse: Response? = null
        var callbackError: IOException? = null
        val request = Request.Builder().url("http://sidekick/tasks").build()

        IrohHttpCallFactory("ticket", FakeIrohConnector(stream)).newCall(request).enqueue(
            object : Callback {
                override fun onFailure(call: okhttp3.Call, e: IOException) {
                    callbackError = e
                    callbackCalled.countDown()
                }

                override fun onResponse(call: okhttp3.Call, response: Response) {
                    callbackResponse = response
                    response.close()
                    callbackCalled.countDown()
                }
            },
        )

        assertTrue(callbackCalled.await(2, TimeUnit.SECONDS))
        assertEquals(200, callbackResponse?.code)
        assertEquals(null, callbackError)
        assertTrue(stream.closed)
    }

    @Test
    fun `cancel closes active resources and reports failure`() {
        val stream = BlockingIrohStream()
        val connector = FakeIrohConnector(stream)
        val call = IrohHttpCallFactory("ticket", connector)
            .newCall(Request.Builder().url("http://sidekick/tasks").build())
        val callbackCalled = CountDownLatch(1)

        call.enqueue(
            object : Callback {
                override fun onFailure(call: okhttp3.Call, e: IOException) {
                    callbackCalled.countDown()
                }

                override fun onResponse(call: okhttp3.Call, response: Response) {
                    response.close()
                    callbackCalled.countDown()
                }
            },
        )
        assertTrue(stream.started.await(2, TimeUnit.SECONDS))
        call.cancel()

        assertTrue(call.isCanceled())
        assertTrue(callbackCalled.await(2, TimeUnit.SECONDS))
        assertTrue(stream.closed)
        assertTrue(connector.connection.closed)
    }

    @Test
    fun `cancel before execution prevents connection`() {
        val connector = FakeIrohConnector(
            FakeIrohStream("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"),
        )
        val call = IrohHttpCallFactory("ticket", connector)
            .newCall(Request.Builder().url("http://sidekick/tasks").build())

        call.cancel()

        assertFailsWithIOException { call.execute() }
        assertTrue(call.isCanceled())
        assertEquals(null, connector.ticket)
    }

    @Test
    fun `cancel continues cleanup when stream close fails`() {
        val stream = BlockingIrohStream(throwOnClose = true)
        val connector = FakeIrohConnector(stream)
        val callbackCalled = CountDownLatch(1)
        val call = IrohHttpCallFactory("ticket", connector)
            .newCall(Request.Builder().url("http://sidekick/tasks").build())

        call.enqueue(
            object : Callback {
                override fun onFailure(call: okhttp3.Call, e: IOException) {
                    callbackCalled.countDown()
                }

                override fun onResponse(call: okhttp3.Call, response: Response) {
                    response.close()
                    callbackCalled.countDown()
                }
            },
        )
        assertTrue(stream.started.await(2, TimeUnit.SECONDS))

        call.cancel()

        assertTrue(callbackCalled.await(2, TimeUnit.SECONDS))
        assertTrue(stream.closed)
        assertTrue(connector.connection.closed)
    }

    @Test
    fun `clone can execute independently from original call`() {
        val streams = ArrayDeque<IrohStream>().apply {
            add(FakeIrohStream("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\none"))
            add(FakeIrohStream("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\ntwo"))
        }
        val connector = IndependentIrohConnector(streams)
        val call = IrohHttpCallFactory("ticket", connector)
            .newCall(Request.Builder().url("http://sidekick/tasks").build())
        val clone = call.clone()

        call.execute().use { response ->
            assertEquals("one", response.body?.string())
        }
        clone.execute().use { response ->
            assertEquals("two", response.body?.string())
        }

        assertTrue(call.isExecuted())
        assertTrue(clone.isExecuted())
        assertEquals(2, connector.connections.size)
    }

    @Test
    fun `connection and stream failures close resources`() {
        assertFailsWithIOException {
            IrohHttpCallFactory(
                "ticket",
                FailingIrohConnector(),
            ).newCall(Request.Builder().url("http://sidekick/tasks").build()).execute()
        }

        listOf("write", "finish", "read").forEach { failure ->
            val stream = FailingIrohStream(failure)
            assertFailsWithIOException {
                IrohHttpCallFactory("ticket", FakeIrohConnector(stream))
                    .newCall(Request.Builder().url("http://sidekick/tasks").build())
                    .execute()
            }
            assertTrue(stream.closed)
        }
    }

    private fun assertFailsWithIOException(block: () -> Unit) {
        try {
            block()
            throw AssertionError("Expected IOException")
        } catch (_: IOException) {
        }
    }

    private class IndependentIrohConnector(
        private val streams: ArrayDeque<IrohStream>,
    ) : IrohConnector {
        val connections = mutableListOf<FakeIrohConnection>()

        override suspend fun connect(ticket: String): IrohConnection {
            val connection = FakeIrohConnection(streams.removeFirst())
            connections += connection
            return connection
        }
    }

    private class FailingIrohConnector : IrohConnector {
        override suspend fun connect(ticket: String): IrohConnection {
            throw IOException("connection failed")
        }
    }

    private class FakeIrohConnector(
        stream: IrohStream,
    ) : IrohConnector {
        val connection = FakeIrohConnection(stream)
        var ticket: String? = null

        override suspend fun connect(ticket: String): IrohConnection {
            this.ticket = ticket
            return connection
        }
    }

    private class FakeIrohConnection(
        private val stream: IrohStream,
    ) : IrohConnection {
        var closed = false

        override suspend fun openBidirectionalStream(): IrohStream = stream

        override fun close() {
            closed = true
        }
    }

    private class FakeIrohStream(
        response: String,
        private val throwOnClose: Boolean = false,
    ) : IrohStream {
        private val responses = ArrayDeque<ByteArray>().apply {
            add(response.encodeToByteArray())
        }
        var written = byteArrayOf()
        var finished = false
        var closed = false

        override suspend fun write(bytes: ByteArray) {
            written += bytes
        }

        override suspend fun finish() {
            finished = true
        }

        override suspend fun read(): ByteArray? =
            if (responses.isEmpty()) null else responses.removeFirst()

        override fun close() {
            closed = true
            if (throwOnClose) {
                throw IllegalStateException("stream close failed")
            }
        }
    }

    private class OpenFailingIrohConnection : IrohConnection {
        var closed = false

        override suspend fun openBidirectionalStream(): IrohStream {
            throw IOException("stream open failed")
        }

        override fun close() {
            closed = true
        }
    }

    private class BlockingIrohStream(
        private val throwOnClose: Boolean = false,
    ) : IrohStream {
        val started = CountDownLatch(1)

        @Volatile
        var closed = false

        override suspend fun write(bytes: ByteArray) = Unit

        override suspend fun finish() = Unit

        override suspend fun read(): ByteArray? {
            started.countDown()
            while (!closed) {
                Thread.sleep(10)
            }
            return null
        }

        override fun close() {
            closed = true
            if (throwOnClose) {
                throw IllegalStateException("stream close failed")
            }
        }
    }

    private class FailingIrohStream(
        private val failure: String,
    ) : IrohStream {
        var closed = false

        override suspend fun write(bytes: ByteArray) {
            if (failure == "write") throw IOException("write failed")
        }

        override suspend fun finish() {
            if (failure == "finish") throw IOException("finish failed")
        }

        override suspend fun read(): ByteArray? {
            if (failure == "read") throw IOException("read failed")
            return null
        }

        override fun close() {
            closed = true
        }
    }
}