package com.example.app

import androidx.compose.material3.SnackbarHostState
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.example.app.core.remote.Task
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.taskdetail.TaskDetailScreen
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
class TaskDetailScreenJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val task = Task(
        id = "t1",
        workspaceId = "ws",
        title = "Fix flaky pairing test",
        description = "The Robolectric test intermittently fails because the scanner result arrives late.",
        status = "in_review",
    )

    @Test
    fun showsTitleStatusDescriptionAndComingSoonNote() {
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                TaskDetailScreen(task = task, isLoading = false, onBack = {})
            }
        }

        composeRule.onNodeWithTag("detail-title").assertTextEquals(task.title)
        composeRule.onNodeWithTag("detail-status").assertTextEquals("In review")
        composeRule.onNodeWithTag("detail-description").assertTextEquals(task.description)
        composeRule.onNodeWithTag("detail-coming-soon").assertTextContains("Coming soon", substring = true)
    }

    @Test
    fun actionsShowNotAvailableSnackbar() {
        val snackbarHostState = SnackbarHostState()
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                TaskDetailScreen(
                    task = task,
                    isLoading = false,
                    onBack = {},
                    snackbarHostState = snackbarHostState,
                )
            }
        }

        for (tag in listOf("detail-edit", "detail-cancel", "detail-archive")) {
            composeRule.onNodeWithText("Not available yet").assertDoesNotExist()
            composeRule.onNodeWithTag(tag).assertIsDisplayed().performClick()
            composeRule.onNodeWithText("Not available yet").assertIsDisplayed()
            composeRule.runOnIdle { snackbarHostState.currentSnackbarData?.dismiss() }
        }
    }

    @Test
    fun backArrowInvokesCallback() {
        var backs = 0
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                TaskDetailScreen(task = task, isLoading = false, onBack = { backs += 1 })
            }
        }

        composeRule.onNodeWithTag("detail-back").performClick()

        assertEquals(1, backs)
    }

    @Test
    fun missingTaskShowsProgressWhileLoadingThenAMessage() {
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                TaskDetailScreen(task = null, isLoading = true, onBack = {})
            }
        }
        composeRule.onNodeWithTag("detail-loading").assertIsDisplayed()
        composeRule.onNodeWithTag("detail-missing").assertDoesNotExist()
    }

    @Test
    fun missingTaskShowsMessageWhenNotLoading() {
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                TaskDetailScreen(task = null, isLoading = false, onBack = {})
            }
        }
        composeRule.onNodeWithTag("detail-missing").assertIsDisplayed()
        composeRule.onNodeWithTag("detail-edit").assertDoesNotExist()
    }
}