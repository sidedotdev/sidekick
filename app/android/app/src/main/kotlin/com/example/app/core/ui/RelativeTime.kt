package com.example.app.core.ui

import java.time.Duration
import java.time.Instant
import java.time.LocalDateTime
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.time.format.DateTimeParseException

private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 60L * SECONDS_PER_MINUTE
private const val SECONDS_PER_DAY = 24L * SECONDS_PER_HOUR
private const val SECONDS_PER_WEEK = 7L * SECONDS_PER_DAY
private const val SECONDS_PER_MONTH = 30L * SECONDS_PER_DAY
private const val SECONDS_PER_YEAR = 365L * SECONDS_PER_DAY

/**
 * Compact "time since" label for an ISO-8601 timestamp, or "" when it cannot be parsed.
 * Timestamps in the future (clock skew) read as "just now".
 */
fun relativeTime(isoUpdated: String, now: Instant = Instant.now()): String {
    val updated = parseInstant(isoUpdated) ?: return ""
    val seconds = Duration.between(updated, now).seconds
    return when {
        seconds < SECONDS_PER_MINUTE -> "just now"
        seconds < SECONDS_PER_HOUR -> "${seconds / SECONDS_PER_MINUTE}m ago"
        seconds < SECONDS_PER_DAY -> "${seconds / SECONDS_PER_HOUR}h ago"
        seconds < SECONDS_PER_WEEK -> "${seconds / SECONDS_PER_DAY}d ago"
        seconds < SECONDS_PER_MONTH -> "${seconds / SECONDS_PER_WEEK}w ago"
        seconds < SECONDS_PER_YEAR -> "${seconds / SECONDS_PER_MONTH}mo ago"
        else -> "${seconds / SECONDS_PER_YEAR}y ago"
    }
}

private fun parseInstant(value: String): Instant? {
    val trimmed = value.trim()
    if (trimmed.isEmpty()) return null
    return try {
        OffsetDateTime.parse(trimmed).toInstant()
    } catch (_: DateTimeParseException) {
        try {
            // Timestamps without an offset are assumed to be UTC, as the server emits.
            LocalDateTime.parse(trimmed).toInstant(ZoneOffset.UTC)
        } catch (_: DateTimeParseException) {
            null
        }
    }
}