package com.example.app

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.example.app.core.remote.Task
import com.example.app.feature.tasks.TaskListScreen
import com.example.app.feature.tasks.TaskListUiState
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class TaskListScreenJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun loadingStateShowsProgress() {
        composeRule.setContent {
            TaskListScreen(
                state = TaskListUiState(isLoading = true),
                onRetry = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("tasks-loading").assertIsDisplayed()
    }

    @Test
    fun emptyStateShowsMessage() {
        composeRule.setContent {
            TaskListScreen(
                state = TaskListUiState(isLoading = false),
                onRetry = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("empty-tasks")
            .assertTextEquals("No tasks are available.")
    }

    @Test
    fun populatedStateShowsTaskContent() {
        composeRule.setContent {
            TaskListScreen(
                state = TaskListUiState(
                    isLoading = false,
                    tasks = listOf(
                        Task(
                            id = "task-1",
                            workspaceId = "workspace-1",
                            title = "Review changes",
                            description = "Review the pending implementation.",
                            status = "in_progress",
                        ),
                    ),
                ),
                onRetry = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("task-task-1").assertIsDisplayed()
        composeRule.onNodeWithText("Review changes").assertIsDisplayed()
        composeRule.onNodeWithTag("task-task-1-status")
            .assertTextEquals("in_progress")
        composeRule.onNodeWithText("Review the pending implementation.")
            .assertIsDisplayed()
    }

    @Test
    fun taskWithoutDescriptionOmitsDescriptionContent() {
        composeRule.setContent {
            TaskListScreen(
                state = TaskListUiState(
                    isLoading = false,
                    tasks = listOf(
                        Task(
                            id = "task-2",
                            workspaceId = "workspace-1",
                            title = "Sync changes",
                            status = "completed",
                        ),
                    ),
                ),
                onRetry = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("task-task-2").assertIsDisplayed()
        composeRule.onNodeWithText("Sync changes").assertIsDisplayed()
        composeRule.onNodeWithTag("task-task-2-status")
            .assertTextEquals("completed")
        composeRule.onNodeWithTag("task-task-2-description")
            .assertDoesNotExist()
    }

    @Test
    fun errorStateOffersRetry() {
        var retries = 0
        composeRule.setContent {
            TaskListScreen(
                state = TaskListUiState(
                    isLoading = false,
                    errorMessage = "Tasks could not be loaded.",
                ),
                onRetry = { retries += 1 },
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("tasks-error")
            .assertTextEquals("Tasks could not be loaded.")
        composeRule.onNodeWithTag("retry-tasks").performClick()

        assertEquals(1, retries)
    }

    @Test
    fun backButtonInvokesCallback() {
        var backRequests = 0
        composeRule.setContent {
            TaskListScreen(
                state = TaskListUiState(isLoading = false),
                onRetry = {},
                onBack = { backRequests += 1 },
            )
        }

        composeRule.onNodeWithTag("back-to-workspaces").performClick()

        assertEquals(1, backRequests)
    }
}