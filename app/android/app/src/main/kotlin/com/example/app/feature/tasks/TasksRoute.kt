package com.example.app.feature.tasks

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.example.app.core.remote.Task

/**
 * Stateful Tasks landing screen. The [viewModel] is owned by the caller so that the task
 * detail route can share it and returning from detail keeps the list's scroll and search state.
 */
@Composable
fun TasksRoute(
    viewModel: TasksViewModel,
    onTaskClick: (Task) -> Unit,
    onScanDifferentCode: () -> Unit,
    onCredentialsMissing: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    LaunchedEffect(state.credentialsMissing) {
        if (state.credentialsMissing) onCredentialsMissing()
    }

    TasksScreen(
        state = state,
        onRefresh = viewModel::onRefresh,
        onRetryTasks = viewModel::onRetryTasks,
        onRetryWorkspaces = viewModel::onRetryWorkspaces,
        onWorkspaceSelected = viewModel::onWorkspaceSelected,
        onSwitcherQueryChanged = viewModel::onSwitcherQueryChanged,
        onSearchQueryChanged = viewModel::onSearchQueryChanged,
        onSearchActiveChanged = viewModel::onSearchActiveChanged,
        onToggleDrafts = viewModel::onToggleDrafts,
        onToggleDone = viewModel::onToggleDone,
        onTaskClick = onTaskClick,
        onScanDifferentCode = onScanDifferentCode,
        modifier = modifier,
    )
}