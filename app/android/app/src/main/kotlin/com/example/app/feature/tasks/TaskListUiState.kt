package com.example.app.feature.tasks

import com.example.app.core.remote.Task

data class TaskListUiState(
    val isLoading: Boolean = true,
    val tasks: List<Task> = emptyList(),
    val errorMessage: String? = null,
)