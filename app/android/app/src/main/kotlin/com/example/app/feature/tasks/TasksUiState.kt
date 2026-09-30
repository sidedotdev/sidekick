package com.example.app.feature.tasks

import com.example.app.core.remote.Task
import com.example.app.core.remote.Workspace

data class TasksUiState(
    val credentialsMissing: Boolean = false,
    val workspaces: List<Workspace> = emptyList(),
    val isLoadingWorkspaces: Boolean = true,
    val workspacesError: String? = null,
    val currentWorkspace: Workspace? = null,
    val tasks: List<Task> = emptyList(),
    val isLoadingTasks: Boolean = false,
    val isRefreshing: Boolean = false,
    val tasksError: String? = null,
    val searchQuery: String = "",
    val isSearchActive: Boolean = false,
    val draftsExpanded: Boolean = false,
    val doneExpanded: Boolean = false,
    val switcherQuery: String = "",
) {
    val bucketed: BucketedTasks
        get() = bucketTasks(tasks)

    val searchResults: List<Task>
        get() = searchTasks(tasks, searchQuery)

    val filteredWorkspaces: List<Workspace>
        get() {
            val needle = switcherQuery.trim()
            if (needle.isEmpty()) return workspaces
            return workspaces.filter { it.name.contains(needle, ignoreCase = true) }
        }
}