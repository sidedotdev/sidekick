package com.example.app.core.remote

import computer.iroh.BiStream
import computer.iroh.Connection
import computer.iroh.Endpoint
import computer.iroh.EndpointOptions
import computer.iroh.EndpointTicket
import computer.iroh.RecvStream
import computer.iroh.SendStream

private const val READ_BUFFER_SIZE = 64 * 1024
private const val SIDEKICK_ALPN = "sidekick/rest/0"

interface IrohConnector {
    suspend fun connect(ticket: String): IrohConnection
}

interface IrohConnection : AutoCloseable {
    suspend fun openBidirectionalStream(): IrohStream
}

interface IrohStream : AutoCloseable {
    suspend fun write(bytes: ByteArray)

    suspend fun finish()

    suspend fun read(): ByteArray?
}

class FfiIrohConnector : IrohConnector {
    override suspend fun connect(ticket: String): IrohConnection {
        val endpoint = Endpoint.bind(EndpointOptions())
        try {
            val endpointTicket = EndpointTicket.fromString(ticket)
            val endpointAddress = try {
                endpointTicket.endpointAddr()
            } finally {
                endpointTicket.close()
            }
            val connection = endpoint.connect(
                endpointAddress,
                SIDEKICK_ALPN.encodeToByteArray(),
            )
            return FfiIrohConnection(endpoint, connection)
        } catch (error: Throwable) {
            endpoint.close()
            throw error
        }
    }
}

private class FfiIrohConnection(
    private val endpoint: Endpoint,
    private val connection: Connection,
) : IrohConnection {
    override suspend fun openBidirectionalStream(): IrohStream =
        FfiIrohStream(connection.openBi())

    override fun close() {
        connection.close()
        endpoint.close()
    }
}

private class FfiIrohStream(
    private val stream: BiStream,
) : IrohStream {
    private val send: SendStream = stream.send()
    private val receive: RecvStream = stream.recv()

    override suspend fun write(bytes: ByteArray) {
        send.writeAll(bytes)
    }

    override suspend fun finish() {
        send.finish()
    }

    override suspend fun read(): ByteArray? =
        receive.read(READ_BUFFER_SIZE.toUInt()).takeUnless(ByteArray::isEmpty)

    override fun close() {
        stream.close()
    }
}