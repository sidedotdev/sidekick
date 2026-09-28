@file:OptIn(ExperimentalMaterial3Api::class)

package com.example.app.feature.taskdetail

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.example.app.core.remote.Task
import com.example.app.core.ui.TaskStatusChip
import com.example.app.core.ui.relativeTime
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.TasksViewModel
import kotlinx.coroutines.launch

private const val NOT_AVAILABLE_MESSAGE = "Not available yet"

/** Reads the task from the [TasksViewModel] shared with the list so no second request is needed. */
@Composable
fun TaskDetailRoute(
    workspaceId: String,
    taskId: String,
    viewModel: TasksViewModel,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    TaskDetailScreen(
        task = state.tasks.find { it.workspaceId == workspaceId && it.id == taskId },
        isLoading = state.isLoadingTasks,
        onBack = onBack,
        modifier = modifier,
    )
}

@Composable
fun TaskDetailScreen(
    task: Task?,
    isLoading: Boolean,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    snackbarHostState: SnackbarHostState = remember { SnackbarHostState() },
) {
    val scope = rememberCoroutineScope()
    val showNotAvailable: () -> Unit = {
        scope.launch { snackbarHostState.showSnackbar(NOT_AVAILABLE_MESSAGE) }
    }

    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text("Task") },
                navigationIcon = {
                    IconButton(onClick = onBack, modifier = Modifier.testTag("detail-back")) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        val contentModifier = Modifier
            .fillMaxSize()
            .padding(padding)
        when {
            task != null -> TaskDetailContent(task, showNotAvailable, contentModifier)
            isLoading -> Box(contentModifier, contentAlignment = Alignment.Center) {
                CircularProgressIndicator(modifier = Modifier.testTag("detail-loading"))
            }

            else -> Box(contentModifier.padding(24.dp), contentAlignment = Alignment.Center) {
                Text(
                    text = "This task is no longer in the list.",
                    textAlign = TextAlign.Center,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.testTag("detail-missing"),
                )
            }
        }
    }
}

@Composable
private fun TaskDetailContent(
    task: Task,
    onAction: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            text = task.title,
            style = MaterialTheme.typography.titleLarge,
            modifier = Modifier.testTag("detail-title"),
        )
        Row(
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TaskStatusChip(status = task.status, modifier = Modifier.testTag("detail-status"))
            relativeTime(task.updated).takeIf { it.isNotEmpty() }?.let { updated ->
                Text(
                    text = "Updated $updated",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = onAction, modifier = Modifier.testTag("detail-edit")) {
                Text("Edit")
            }
            OutlinedButton(onClick = onAction, modifier = Modifier.testTag("detail-cancel")) {
                Text("Cancel")
            }
            OutlinedButton(onClick = onAction, modifier = Modifier.testTag("detail-archive")) {
                Text("Archive")
            }
        }
        ComingSoonNote(modifier = Modifier.testTag("detail-coming-soon"))
        Text(
            text = "Description",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (task.description.isBlank()) {
            Text(
                text = "No description.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.testTag("detail-description"),
            )
        } else {
            Text(
                text = task.description,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.testTag("detail-description"),
            )
        }
    }
}

@Composable
private fun ComingSoonNote(modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant,
        shape = MaterialTheme.shapes.medium,
        modifier = modifier
            .fillMaxWidth()
            .semantics(mergeDescendants = true) {},
    ) {
        Column(
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            Text(text = "Coming soon", style = MaterialTheme.typography.labelLarge)
            Text(
                text = "Task activity, editing and other actions will land here. For now this is a read-only preview of the task.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

internal fun sampleDetailTask(): Task = Task(
    id = "t1",
    workspaceId = "ws",
    title = "Return the pairing screen to a scan-only flow",
    description = "Drop the workspace list from pairing and land directly on the tasks screen. " +
        "Keep the instrumented entry test green and add a regression for rescanning.",
    status = "in_review",
    updated = "2026-09-28T10:00:00Z",
)

@Preview(name = "Task detail light", showBackground = true)
@Composable
private fun TaskDetailPreview() {
    AppTheme(darkTheme = false) {
        TaskDetailScreen(task = sampleDetailTask(), isLoading = false, onBack = {})
    }
}

@Preview(name = "Task detail dark", showBackground = true)
@Composable
private fun TaskDetailDarkPreview() {
    AppTheme(darkTheme = true) {
        TaskDetailScreen(task = sampleDetailTask(), isLoading = false, onBack = {})
    }
}