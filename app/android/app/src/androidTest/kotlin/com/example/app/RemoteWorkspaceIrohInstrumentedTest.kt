package com.example.app

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.example.app.core.remote.FfiIrohConnector
import com.example.app.core.remote.IrohConnection
import com.example.app.core.remote.IrohStream
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.SidekickRemoteSession
import java.io.ByteArrayOutputStream
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

private const val CONNECT_TIMEOUT_MS = 30_000L
private const val OPEN_STREAM_TIMEOUT_MS = 20_000L
private const val WRITE_TIMEOUT_MS = 20_000L
private const val FIRST_BYTE_TIMEOUT_MS = 30_000L
private const val EOF_TIMEOUT_MS = 15_000L
private const val API_TIMEOUT_MS = 60_000L

/**
 * Live on-device coverage of the remote access path: it dials the sidekick
 * server over iroh through the same [FfiIrohConnector] the app uses and reads
 * the workspace list, first stage by stage over a raw stream and then through
 * the full Retrofit stack.
 *
 * It needs pairing credentials for a running server, supplied as
 * instrumentation arguments by `scripts/android_phone_remote_e2e/run.sh`; the
 * test is skipped without them so plain `connectedDebugAndroidTest` runs stay
 * self-contained.
 *
 * Stage timeouts only fire at coroutine suspension points, so a native call
 * that blocks its thread outright is bounded by the runner script's hard
 * timeout instead. Every stage is mirrored to logcat under [LOG_TAG] both when
 * it starts and when it settles, so a stage logged as started without an
 * outcome identifies where a hang occurred.
 */
@RunWith(AndroidJUnit4::class)
class RemoteWorkspaceIrohInstrumentedTest {

    @Test
    fun workspacesLoadOverIroh() {
        val arguments = InstrumentationRegistry.getArguments()
        val ticket = arguments.getString("sidekickTicket").orEmpty().trim()
        val token = arguments.getString("sidekickToken").orEmpty().trim()
        assumeTrue(
            "Requires live pairing credentials; run scripts/android_phone_remote_e2e/run.sh",
            ticket.isNotEmpty() && token.isNotEmpty(),
        )

        val stages = StageLog(secrets = listOf(ticket, token))
        try {
            runBlocking {
                probeRawTransport(stages, ticket, token)

                val session = stages.stage("api-build-client", API_TIMEOUT_MS) {
                    SidekickRemoteSession(
                        PairingCredentials(ticket = ticket, token = token),
                        FfiIrohConnector(),
                    )
                }
                try {
                    val workspaces = stages.stage("api-get-workspaces", API_TIMEOUT_MS) {
                        session.api.getWorkspaces().workspaces
                    }
                    stages.note("api returned ${workspaces.size} workspace(s)")
                } finally {
                    stages.closeQuietly("remote-session", session)
                }
            }
        } catch (error: Throwable) {
            // Re-thrown redacted: transport errors quote the ticket, and request
            // framing errors can quote the header line carrying the token.
            throw AssertionError(
                stages.redact("remote workspace probe failed: ${error.describe()}\n$stages"),
            )
        } finally {
            stages.dumpSummary()
        }
    }
}

private suspend fun probeRawTransport(stages: StageLog, ticket: String, token: String) {
    var connection: IrohConnection? = null
    var stream: IrohStream? = null
    try {
        val openedConnection = stages.stage("transport-connect", CONNECT_TIMEOUT_MS) {
            FfiIrohConnector(onStage = stages::record).connect(ticket)
        }
        connection = openedConnection

        val openedStream = stages.stage("transport-open-stream", OPEN_STREAM_TIMEOUT_MS) {
            openedConnection.openBidirectionalStream()
        }
        stream = openedStream

        stages.stage("transport-write-request", WRITE_TIMEOUT_MS) {
            openedStream.write(rawWorkspaceRequest(token))
        }

        val firstChunk = stages.stage("transport-first-response-chunk", FIRST_BYTE_TIMEOUT_MS) {
            openedStream.read()
        }
        assertNotNull("Server closed the stream without sending any response", firstChunk)

        val received = ByteArrayOutputStream()
        received.write(firstChunk)
        val sawEof = stages.softStage("transport-read-to-eof", EOF_TIMEOUT_MS) {
            while (true) {
                val chunk = openedStream.read() ?: break
                received.write(chunk)
            }
            true
        } ?: false

        val response = received.toByteArray()
        stages.note("received ${response.size} response byte(s), eofObserved=$sawEof")
        val statusLine = statusLine(response)
        stages.note("status line: $statusLine")
        if (statusLine.startsWith("HTTP/1.1 401")) {
            val body = response.toString(Charsets.UTF_8).substringAfter("\r\n\r\n", "")
            val category = when (body) {
                "{\"error\":\"missing device token\"}" -> "missing"
                "{\"error\":\"invalid device token\"}" -> "invalid"
                else -> "unrecognized"
            }
            stages.note("authentication failure category: $category")
        }
        assertTrue(
            "Expected a 200 OK response over the raw iroh stream, got: $statusLine",
            statusLine.startsWith("HTTP/1.1 200"),
        )
    } finally {
        stages.closeQuietly("transport-close-stream", stream)
        stages.closeQuietly("transport-close-connection", connection)
    }
}

private fun rawWorkspaceRequest(token: String): ByteArray =
    buildString {
        append("GET /api/v1/workspaces HTTP/1.1\r\n")
        append("Host: sidekick\r\n")
        append("Authorization: Bearer ").append(token).append("\r\n")
        append("Accept: application/json\r\n")
        append("Connection: close\r\n")
        append("\r\n")
    }.toByteArray(Charsets.UTF_8)

private fun statusLine(response: ByteArray): String {
    val text = response.decodeToString()
    return text.substringBefore("\r\n").ifEmpty { "<empty>" }
}