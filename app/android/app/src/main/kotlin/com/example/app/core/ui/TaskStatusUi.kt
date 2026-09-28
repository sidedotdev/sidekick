package com.example.app.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.example.app.core.ui.theme.AmberDark
import com.example.app.core.ui.theme.AmberLight
import com.example.app.core.ui.theme.GreenDark
import com.example.app.core.ui.theme.GreenLight

fun statusLabel(status: String): String = when (status) {
    "drafting" -> "Draft"
    "to_do" -> "To do"
    "in_progress" -> "In progress"
    "blocked" -> "Blocked"
    "in_review" -> "In review"
    "complete" -> "Complete"
    "failed" -> "Failed"
    "canceled" -> "Canceled"
    else -> humanizeStatus(status)
}

private fun humanizeStatus(status: String): String {
    val words = status.split('_', '-', ' ').filter { it.isNotBlank() }
    if (words.isEmpty()) return "Unknown"
    return words.joinToString(" ") { it.lowercase() }.replaceFirstChar { it.uppercase() }
}

fun statusColor(status: String, colorScheme: ColorScheme): Color {
    val darkTheme = colorScheme.background.luminance() < 0.5f
    return when (status) {
        "blocked", "failed" -> colorScheme.error
        "in_review" -> if (darkTheme) AmberDark else AmberLight
        "in_progress" -> colorScheme.primary
        "complete" -> if (darkTheme) GreenDark else GreenLight
        else -> colorScheme.onSurfaceVariant
    }
}

/**
 * Coloured status dot followed by a muted human-readable label.
 * Descendants are merged so a test tag on [modifier] exposes the label text.
 */
@Composable
fun TaskStatusChip(
    status: String,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(7.dp)
                .background(statusColor(status, MaterialTheme.colorScheme), CircleShape),
        )
        Text(
            text = statusLabel(status),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}