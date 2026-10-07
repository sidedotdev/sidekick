package com.example.app.core.ui

import com.example.app.core.ui.theme.appColorScheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

class TaskStatusUiTest {

    @Test
    fun `known statuses map to human readable labels`() {
        val cases = mapOf(
            "drafting" to "Draft",
            "to_do" to "To do",
            "in_progress" to "In progress",
            "blocked" to "Blocked",
            "in_review" to "In review",
            "complete" to "Complete",
            "failed" to "Failed",
            "canceled" to "Canceled",
        )

        cases.forEach { (status, expected) ->
            assertEquals(status, expected, statusLabel(status))
        }
    }

    @Test
    fun `unknown statuses are humanized`() {
        val cases = mapOf(
            "completed" to "Completed",
            "needs_input" to "Needs input",
            "AWAITING-MERGE" to "Awaiting merge",
            "__odd__" to "Odd",
            "" to "Unknown",
            "   " to "Unknown",
        )

        cases.forEach { (status, expected) ->
            assertEquals(status, expected, statusLabel(status))
        }
    }

    @Test
    fun `status colours distinguish attention from neutral statuses`() {
        listOf(false, true).forEach { darkTheme ->
            val scheme = appColorScheme(darkTheme)
            val label = "dark=$darkTheme"

            assertEquals(label, scheme.error, statusColor("blocked", scheme))
            assertEquals(label, scheme.error, statusColor("failed", scheme))
            assertEquals(label, scheme.primary, statusColor("in_progress", scheme))
            assertEquals(label, scheme.onSurfaceVariant, statusColor("to_do", scheme))
            assertEquals(label, scheme.onSurfaceVariant, statusColor("drafting", scheme))
            assertEquals(label, scheme.onSurfaceVariant, statusColor("canceled", scheme))
            assertEquals(label, scheme.onSurfaceVariant, statusColor("mystery", scheme))
            assertNotEquals(label, scheme.onSurfaceVariant, statusColor("in_review", scheme))
            assertNotEquals(label, scheme.onSurfaceVariant, statusColor("complete", scheme))
            assertNotEquals(label, statusColor("in_review", scheme), statusColor("complete", scheme))
        }
    }

    @Test
    fun `status colours follow the light or dark scheme`() {
        val light = appColorScheme(darkTheme = false)
        val dark = appColorScheme(darkTheme = true)

        listOf("in_review", "complete", "blocked").forEach { status ->
            assertNotEquals(status, statusColor(status, light), statusColor(status, dark))
        }
    }
}