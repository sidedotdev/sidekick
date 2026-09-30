package com.example.app

import android.os.SystemClock
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.example.app.core.remote.FfiIrohConnector
import com.example.app.core.remote.describeCauseChain
import java.io.ByteArrayOutputStream
import java.security.MessageDigest
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Counterpart to scripts/iroh_transfer_probe, independent of app credentials and API data. */
@RunWith(AndroidJUnit4::class)
class IrohTransferInstrumentedTest {
    @Test
    fun verifiesCompletePayloadOverNativeStream() = runBlocking {
        val args = InstrumentationRegistry.getArguments()
        val ticket = args.getString("sidekickProbeTicket").orEmpty()
        assumeTrue("Requires an isolated transfer probe", ticket.isNotEmpty())
        val size = checkNotNull(args.getString("sidekickProbeBytes")).toInt()
        val digest = checkNotNull(args.getString("sidekickProbeSha256"))
        val rounds = args.getString("sidekickProbeRounds")?.toInt() ?: 3
        require(size in 1..64 * 1024 * 1024 && rounds in 1..20)
        val stages = StageLog(secrets = listOf(ticket))
        try {
            ActivityScenario.launch(MainActivity::class.java).use {
                withTimeout(15_000) { FfiIrohConnector().connect(ticket) }.use { connection ->
                    repeat(rounds) { round ->
                        var received = 0
                        val started = SystemClock.elapsedRealtime()
                        try {
                            withTimeout(25_000) {
                                connection.openBidirectionalStream().use { stream ->
                                    stages.note("round=${round + 1} stream opened paths=${connection.pathSummary()}")
                                    stream.write("GET / HTTP/1.1\r\nHost: probe\r\nConnection: close\r\n\r\n".encodeToByteArray())
                                    stages.note("round=${round + 1} request written")
                                    val header = ByteArrayOutputStream()
                                    val checksum = MessageDigest.getInstance("SHA-256")
                                    var headerComplete = false
                                    var headerSuffix = 0
                                    var bodyBytes = 0
                                    var nextReport = 16 * 1024
                                    while (true) {
                                        val chunk = stream.read() ?: break
                                        received += chunk.size
                                        check(received <= size + 4096) { "Response exceeded expected bound" }
                                        var offset = 0
                                        while (!headerComplete && offset < chunk.size) {
                                            check(header.size() < 4096) { "HTTP headers exceeded expected bound" }
                                            val byte = chunk[offset++].toInt() and 0xff
                                            header.write(byte)
                                            headerSuffix = (headerSuffix shl 8) or byte
                                            headerComplete = headerSuffix == 0x0d0a0d0a
                                        }
                                        if (offset < chunk.size) {
                                            checksum.update(chunk, offset, chunk.size - offset)
                                            bodyBytes += chunk.size - offset
                                        }
                                        if (received >= nextReport) {
                                            stages.note("round=${round + 1} received=$received paths=${connection.pathSummary()}")
                                            nextReport = received + 1024 * 1024
                                        }
                                    }
                                    check(headerComplete) { "Missing HTTP header terminator" }
                                    check(header.toString("UTF-8").startsWith("HTTP/1.1 200 OK\r\n")) {
                                        "Unexpected status"
                                    }
                                    assertEquals("Payload length", size, bodyBytes)
                                    val actual = checksum.digest().joinToString("") { "%02x".format(it) }
                                    assertEquals("Payload checksum", digest, actual)
                                }
                            }
                        } finally {
                            stages.note(
                                "round=${round + 1} finished received=$received " +
                                    "elapsed=${SystemClock.elapsedRealtime() - started}ms paths=${connection.pathSummary()}",
                            )
                        }
                    }
                }
            }
        } catch (error: Exception) {
            throw AssertionError(stages.redact("${error.describeCauseChain()}\n$stages"))
        } finally {
            stages.dumpSummary()
        }
    }
}
