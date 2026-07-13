package com.example.app.core.remote

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread
import kotlinx.coroutines.runBlocking
import okio.Buffer
import okio.Timeout
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Headers
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody

private const val MAX_RESPONSE_BYTES = 16 * 1024 * 1024

class IrohHttpCallFactory(
    private val ticket: String,
    private val connector: IrohConnector,
) : Call.Factory {
    override fun newCall(request: Request): Call =
        IrohHttpCall(request, ticket, connector)
}

private class IrohHttpCall(
    private val originalRequest: Request,
    private val ticket: String,
    private val connector: IrohConnector,
) : Call {
    private val executed = AtomicBoolean(false)
    private val canceled = AtomicBoolean(false)

    @Volatile
    private var activeConnection: IrohConnection? = null

    @Volatile
    private var activeStream: IrohStream? = null

    override fun request(): Request = originalRequest

    override fun execute(): Response {
        check(executed.compareAndSet(false, true)) { "Already Executed" }
        return executeRequest()
    }

    private fun executeRequest(): Response {
        if (canceled.get()) {
            throw IOException("Canceled")
        }

        return runBlocking {
            var connection: IrohConnection? = null
            var stream: IrohStream? = null
            try {
                val openedConnection = connector.connect(ticket)
                connection = openedConnection
                activeConnection = openedConnection
                if (canceled.get()) {
                    throw IOException("Canceled")
                }

                val openedStream = openedConnection.openBidirectionalStream()
                stream = openedStream
                activeStream = openedStream
                if (canceled.get()) {
                    throw IOException("Canceled")
                }

                openedStream.write(encodeRequest(originalRequest))
                openedStream.finish()

                val responseBytes = ByteArrayOutputStream()
                while (!canceled.get()) {
                    val chunk = openedStream.read() ?: break
                    if (responseBytes.size() + chunk.size > MAX_RESPONSE_BYTES) {
                        throw IOException("Remote response exceeds $MAX_RESPONSE_BYTES bytes")
                    }
                    responseBytes.write(chunk)
                }
                if (canceled.get()) {
                    throw IOException("Canceled")
                }

                decodeResponse(originalRequest, responseBytes.toByteArray())
            } catch (error: IOException) {
                throw error
            } catch (error: Exception) {
                throw IOException("Iroh request failed", error)
            } finally {
                closeQuietly(stream)
                activeStream = null
                closeQuietly(connection)
                activeConnection = null
            }
        }
    }

    override fun enqueue(responseCallback: Callback) {
        if (!executed.compareAndSet(false, true)) {
            throw IllegalStateException("Already Executed")
        }

        thread(name = "sidekick-iroh-http", isDaemon = true) {
            val response = try {
                executeRequest()
            } catch (error: IOException) {
                responseCallback.onFailure(this, error)
                return@thread
            }

            try {
                responseCallback.onResponse(this, response)
            } catch (error: Throwable) {
                response.close()
                throw error
            }
        }
    }

    override fun cancel() {
        if (canceled.compareAndSet(false, true)) {
            closeQuietly(activeStream)
            closeQuietly(activeConnection)
        }
    }

    override fun isExecuted(): Boolean = executed.get()

    override fun isCanceled(): Boolean = canceled.get()

    override fun timeout(): Timeout = Timeout.NONE

    override fun clone(): Call = IrohHttpCall(originalRequest, ticket, connector)
}

private fun encodeRequest(request: Request): ByteArray {
    val requestBody = request.body
    val bodyBuffer = Buffer()
    requestBody?.writeTo(bodyBuffer)
    val body = bodyBuffer.readByteArray()

    val output = Buffer()
    val path = buildString {
        append(request.url.encodedPath)
        request.url.encodedQuery?.let {
            append('?')
            append(it)
        }
    }

    output.writeUtf8("${request.method} $path HTTP/1.1\r\n")
    output.writeUtf8("Host: ${request.url.host}\r\n")
    output.writeUtf8("Connection: close\r\n")

    for ((name, value) in request.headers) {
        if (!name.equals("Host", ignoreCase = true) &&
            !name.equals("Connection", ignoreCase = true) &&
            !name.equals("Content-Length", ignoreCase = true) &&
            !(requestBody != null && name.equals("Content-Type", ignoreCase = true))
        ) {
            output.writeUtf8("$name: $value\r\n")
        }
    }

    if (requestBody != null) {
        requestBody.contentType()?.let { output.writeUtf8("Content-Type: $it\r\n") }
        output.writeUtf8("Content-Length: ${body.size}\r\n")
    }

    output.writeUtf8("\r\n")
    output.write(body)
    return output.readByteArray()
}

private fun decodeResponse(request: Request, bytes: ByteArray): Response {
    val separator = "\r\n\r\n".encodeToByteArray()
    val headerEnd = bytes.indexOf(separator)
    if (headerEnd < 0) {
        throw IOException("Invalid HTTP response: headers are incomplete")
    }

    val headerText = bytes
        .copyOfRange(0, headerEnd)
        .decodeToString()
    val headerLines = headerText.split("\r\n")
    val statusMatch = Regex("""^(HTTP/1\.[01]) ([0-9]{3})(?: (.*))?$""")
        .matchEntire(headerLines.first())
        ?: throw IOException("Invalid HTTP status line")
    val statusCode = statusMatch.groupValues[2].toInt()
    val reason = statusMatch.groups[3]?.value.orEmpty()
    if (reason.any { it == '\r' || it == '\n' || it.code < 0x20 && it != '\t' }) {
        throw IOException("Invalid HTTP status line")
    }

    val headersBuilder = Headers.Builder()
    val contentLengthHeaderValues = mutableListOf<String>()
    var contentLengthHeaderAdded = false
    headerLines.drop(1).forEach { line ->
        val separatorIndex = line.indexOf(':')
        if (separatorIndex <= 0 ||
            !line.substring(0, separatorIndex).matches(Regex("[!#$%&'*+\\-.^_`|~0-9A-Za-z]+"))
        ) {
            throw IOException("Invalid HTTP header")
        }
        val name = line.substring(0, separatorIndex)
        val value = line.substring(separatorIndex + 1)
        if (value.any { it == '\r' || it == '\n' || it.code < 0x20 && it != '\t' }) {
            throw IOException("Invalid HTTP header")
        }
        if (name.equals("Content-Length", ignoreCase = true)) {
            contentLengthHeaderValues += value.trim()
            if (contentLengthHeaderAdded) {
                return@forEach
            }
            contentLengthHeaderAdded = true
        }
        headersBuilder.add(name, value.trim())
    }
    val headers = headersBuilder.build()
    val encodedBody = bytes.copyOfRange(headerEnd + separator.size, bytes.size)
    val transferEncodingHeaders = headers.values("Transfer-Encoding")
    val transferCodings = transferEncodingHeaders
        .flatMap { value -> value.split(',').map { it.trim() } }
    if (transferEncodingHeaders.isNotEmpty() &&
        (transferCodings.isEmpty() ||
            transferCodings.any { coding ->
                coding.isEmpty() || !coding.matches(Regex("[!#$%&'*+\\-.^_`|~0-9A-Za-z]+"))
            } ||
            transferCodings.size != 1 ||
            !transferCodings.last().equals("chunked", ignoreCase = true))
    ) {
        throw IOException("Unsupported or invalid Transfer-Encoding")
    }

    val contentLengthValues = contentLengthHeaderValues
        .flatMap { value -> value.split(',').map { it.trim() } }
    val contentLengths = contentLengthValues.map { value ->
        if (!value.matches(Regex("[0-9]+"))) {
            throw IOException("Invalid response body length")
        }
        value.toLongOrNull() ?: throw IOException("Invalid response body length")
    }
    if (transferCodings.isNotEmpty() && contentLengths.isNotEmpty()) {
        throw IOException("Response contains both Transfer-Encoding and Content-Length")
    }
    if (contentLengths.distinct().size > 1) {
        throw IOException("Conflicting response body lengths")
    }

    val body = if (transferCodings.isNotEmpty()) {
        decodeChunkedBody(encodedBody)
    } else if (contentLengths.isNotEmpty()) {
        if (contentLengths.first() != encodedBody.size.toLong()) {
            throw IOException("Invalid response body length")
        }
        encodedBody
    } else {
        encodedBody
    }

    return Response.Builder()
        .request(request)
        .protocol(
            if (statusMatch.groupValues[1] == "HTTP/1.0") {
                Protocol.HTTP_1_0
            } else {
                Protocol.HTTP_1_1
            },
        )
        .code(statusCode)
        .message(reason)
        .headers(headers)
        .body(body.toResponseBody(headers["Content-Type"]?.toMediaTypeOrNull()))
        .build()
}

private fun readCrlfLine(input: Buffer): String {
    val line = Buffer()
    while (true) {
        if (input.exhausted()) {
            throw IOException("Incomplete chunked response")
        }
        when (val byte = input.readByte()) {
            '\r'.code.toByte() -> {
                if (input.exhausted() || input.readByte() != '\n'.code.toByte()) {
                    throw IOException("Invalid chunk line terminator")
                }
                return line.readUtf8()
            }
            '\n'.code.toByte() -> throw IOException("Invalid chunk line terminator")
            else -> line.writeByte(byte.toInt())
        }
    }
}

private fun decodeChunkedBody(bytes: ByteArray): ByteArray {
    val input = Buffer().write(bytes)
    val output = Buffer()

    try {
        while (true) {
            val sizeLine = readCrlfLine(input)
            val sizeToken = sizeLine.substringBefore(';')
            if (!sizeToken.matches(Regex("[0-9A-Fa-f]+"))) {
                throw IOException("Invalid chunk size")
            }
            val size = sizeToken.toLongOrNull(16)
                ?: throw IOException("Invalid chunk size")
            if (size < 0) {
                throw IOException("Invalid chunk size")
            }
            if (size == 0L) {
                while (true) {
                    val trailer = readCrlfLine(input)
                    if (trailer.isEmpty()) {
                        break
                    }
                    validateHeaderLine(trailer)
                }
                if (!input.exhausted()) {
                    throw IOException("Trailing data after chunked response")
                }
                break
            }
            if (size > input.size) {
                throw IOException("Incomplete chunked response")
            }
            output.write(input, size)
            if (readCrlfLine(input).isNotEmpty()) {
                throw IOException("Invalid chunk terminator")
            }
        }
    } catch (error: IOException) {
        throw error
    } catch (error: Exception) {
        throw IOException("Invalid chunked response", error)
    }

    return output.readByteArray()
}

private fun validateHeaderLine(line: String) {
    val separatorIndex = line.indexOf(':')
    if (separatorIndex <= 0 ||
        !line.substring(0, separatorIndex).matches(Regex("[!#$%&'*+\\-.^_`|~0-9A-Za-z]+"))
    ) {
        throw IOException("Invalid HTTP trailer")
    }
    val value = line.substring(separatorIndex + 1)
    if (value.any { it == '\r' || it == '\n' || it.code < 0x20 && it != '\t' }) {
        throw IOException("Invalid HTTP trailer")
    }
}

private fun closeQuietly(resource: AutoCloseable?) {
    try {
        resource?.close()
    } catch (_: Exception) {
    }
}

private fun ByteArray.indexOf(needle: ByteArray): Int {
    if (needle.isEmpty()) return 0

    for (start in 0..size - needle.size) {
        var matches = true
        for (offset in needle.indices) {
            if (this[start + offset] != needle[offset]) {
                matches = false
                break
            }
        }
        if (matches) return start
    }
    return -1
}