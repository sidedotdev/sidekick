package com.example.app

import android.os.SystemClock
import android.util.Log
import kotlinx.coroutines.withTimeoutOrNull

internal const val LOG_TAG = "SidekickPhoneE2E"

/**
 * Records the progress of each connection stage to logcat and test output,
 * with pairing credentials stripped so captured diagnostics can be shared.
 *
 * Stage timeouts only fire at coroutine suspension points, so a native call
 * that blocks its thread outright is bounded by the runner script's hard
 * timeout instead. Every stage is logged under [LOG_TAG] both when it starts
 * and when it settles, so a stage logged as started without an outcome
 * identifies where a hang occurred.
 */
internal class StageLog(private val secrets: List<String> = emptyList()) {
    private val entries = mutableListOf<String>()

    fun record(entry: String) {
        val safeEntry = redact(entry)
        entries += safeEntry
        Log.i(LOG_TAG, safeEntry)
        println("$LOG_TAG $safeEntry")
    }

    fun start(stage: String) = record("$stage | START")

    fun note(message: String) = record("note | $message")

    fun dumpSummary() = record("stage summary:\n$this")

    fun redact(text: String): String =
        secrets.filter(String::isNotEmpty).fold(text) { redacted, secret ->
            redacted.replace(secret, "<redacted>")
        }

    override fun toString(): String = entries.joinToString(separator = "\n")
}

private class Box<T>(val value: T)

/** Runs a stage, failing the test when it times out or throws. */
internal suspend fun <T> StageLog.stage(
    name: String,
    timeoutMillis: Long,
    block: suspend () -> T,
): T {
    start(name)
    val startedAt = SystemClock.elapsedRealtime()
    val box = try {
        withTimeoutOrNull(timeoutMillis) { Box(block()) }
    } catch (error: Throwable) {
        record("$name | FAILED after ${SystemClock.elapsedRealtime() - startedAt}ms | ${error.describe()}")
        throw AssertionError(redact("Stage '$name' failed\n$this"))
    }

    if (box == null) {
        record("$name | TIMED OUT after ${timeoutMillis}ms")
        throw AssertionError(redact("Stage '$name' timed out after ${timeoutMillis}ms\n$this"))
    }

    record("$name | ok after ${SystemClock.elapsedRealtime() - startedAt}ms")
    return box.value
}

/**
 * Runs a diagnostic stage whose failure is informative rather than fatal, so
 * later stages still get a chance to expose their own behavior.
 */
internal suspend fun <T> StageLog.softStage(
    name: String,
    timeoutMillis: Long,
    block: suspend () -> T,
): T? {
    start(name)
    val startedAt = SystemClock.elapsedRealtime()
    val box = try {
        withTimeoutOrNull(timeoutMillis) { Box(block()) }
    } catch (error: Throwable) {
        record("$name | failed after ${SystemClock.elapsedRealtime() - startedAt}ms | ${error.describe()}")
        return null
    }

    if (box == null) {
        record("$name | timed out after ${timeoutMillis}ms")
        return null
    }

    record("$name | ok after ${SystemClock.elapsedRealtime() - startedAt}ms")
    return box.value
}

internal fun StageLog.closeQuietly(name: String, resource: AutoCloseable?) {
    if (resource == null) {
        return
    }
    start(name)
    val startedAt = SystemClock.elapsedRealtime()
    try {
        resource.close()
        record("$name | ok after ${SystemClock.elapsedRealtime() - startedAt}ms")
    } catch (error: Throwable) {
        record("$name | failed after ${SystemClock.elapsedRealtime() - startedAt}ms | ${error.describe()}")
    }
}

internal fun Throwable.describe(): String =
    generateSequence<Throwable>(this) { current -> current.cause?.takeIf { it !== current } }
        .joinToString(separator = " <- ") { error ->
            "${error.javaClass.name}: ${error.message ?: "<no message>"}"
        }