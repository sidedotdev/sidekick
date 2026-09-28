package com.example.app.core.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class RelativeTimeTest {

    private val now = Instant.parse("2026-03-10T12:00:00Z")

    @Test
    fun `elapsed time is bucketed into compact labels`() {
        val cases = mapOf(
            "2026-03-10T12:00:00Z" to "just now",
            "2026-03-10T11:59:30Z" to "just now",
            "2026-03-10T11:55:00Z" to "5m ago",
            "2026-03-10T11:00:01Z" to "59m ago",
            "2026-03-10T09:00:00Z" to "3h ago",
            "2026-03-09T12:00:01Z" to "23h ago",
            "2026-03-08T12:00:00Z" to "2d ago",
            "2026-03-04T12:00:00Z" to "6d ago",
            "2026-03-03T12:00:00Z" to "1w ago",
            "2026-02-08T12:00:00Z" to "1mo ago",
            "2025-03-10T12:00:00Z" to "1y ago",
        )

        cases.forEach { (updated, expected) ->
            assertEquals(updated, expected, relativeTime(updated, now))
        }
    }

    @Test
    fun `server timestamp formats are accepted`() {
        val cases = mapOf(
            "2026-03-10T11:54:30.123456789Z" to "5m ago",
            "2026-03-10T13:55:00+02:00" to "5m ago",
            "2026-03-10T06:55:00-05:00" to "5m ago",
            "2026-03-10T11:55:00" to "5m ago",
            " 2026-03-10T11:55:00Z " to "5m ago",
        )

        cases.forEach { (updated, expected) ->
            assertEquals(updated, expected, relativeTime(updated, now))
        }
    }

    @Test
    fun `future timestamps read as just now`() {
        assertEquals("just now", relativeTime("2026-03-10T12:05:00Z", now))
    }

    @Test
    fun `unparsable timestamps produce an empty label`() {
        listOf("", "   ", "yesterday", "2026-13-40T00:00:00Z", "1710000000").forEach { updated ->
            assertEquals(updated, "", relativeTime(updated, now))
        }
    }
}