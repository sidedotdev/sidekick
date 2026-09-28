package com.example.app.core.ui.theme

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.example.app.core.ui.TaskStatusChip

private val SampleStatuses = listOf(
    "blocked", "in_review", "in_progress", "to_do",
    "drafting", "complete", "failed", "canceled",
)

/** Gallery of the theme's core components, shared by previews and screenshot tests. */
@Composable
internal fun ThemeSample(modifier: Modifier = Modifier) {
    Surface(
        modifier = modifier.fillMaxSize(),
        color = MaterialTheme.colorScheme.background,
    ) {
        Column(
            modifier = Modifier.padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("Sidekick", style = MaterialTheme.typography.titleLarge)
            Text(
                "Body text sits on a neutral surface with grey tiers for detail.",
                style = MaterialTheme.typography.bodyMedium,
            )
            Text(
                "Secondary detail",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = {}) { Text("Primary") }
                OutlinedButton(onClick = {}) { Text("Secondary") }
                TextButton(onClick = {}) { Text("Text") }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilterChip(selected = true, onClick = {}, label = { Text("Open · 5") })
                FilterChip(selected = false, onClick = {}, label = { Text("Drafts · 2") })
                FilterChip(selected = false, onClick = {}, label = { Text("Done · 9") })
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            SampleStatuses.chunked(4).forEach { statuses ->
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    statuses.forEach { TaskStatusChip(status = it) }
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            SampleTaskRow(
                title = "Redesign the Android tasks screen with a denser list",
                status = "in_review",
                time = "5m ago",
                description = "Bucket tasks by relevance and add a workspace switcher.",
            )
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            SampleTaskRow(
                title = "Fix flaky pairing test",
                status = "in_progress",
                time = "2d ago",
                description = "",
            )
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Text(
                "Something went wrong.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

@Composable
private fun SampleTaskRow(
    title: String,
    status: String,
    time: String,
    description: String,
) {
    Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
            verticalAlignment = Alignment.Top,
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = time,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TaskStatusChip(status = status)
            if (description.isNotBlank()) {
                Text(
                    text = description,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}