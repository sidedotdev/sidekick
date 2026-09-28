package com.example.app

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.TasksScreen
import com.example.app.feature.tasks.bucketTasks
import com.example.app.feature.tasks.sampleTasksUiState
import com.example.app.testing.captureScreenshot
import com.example.app.testing.writeWindowScreenshot
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.ParameterizedRobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Renders the Tasks screen and the workspace picker in light and dark for visual review, and
 * guards the density requirement: at least five whole open rows on a 360×640dp phone.
 */
@RunWith(ParameterizedRobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TasksScreenScreenshotTest(private val darkTheme: Boolean) {

    @get:Rule
    val composeRule = createComposeRule()

    private val mode = if (darkTheme) "dark" else "light"
    private val state = sampleTasksUiState()

    private fun setContent() {
        composeRule.setContent {
            AppTheme(darkTheme = darkTheme) {
                TasksScreen(
                    state = state,
                    onRefresh = {},
                    onRetryTasks = {},
                    onRetryWorkspaces = {},
                    onWorkspaceSelected = {},
                    onSwitcherQueryChanged = {},
                    onSearchQueryChanged = {},
                    onSearchActiveChanged = {},
                    onToggleDrafts = {},
                    onToggleDone = {},
                    onTaskClick = {},
                    onScanDifferentCode = {},
                )
            }
        }
    }

    @Test
    fun rendersListWithAtLeastFiveVisibleRows() {
        setContent()

        val buckets = bucketTasks(state.tasks)
        val openTasks = buckets.needsAttention + buckets.active
        assertTrue("sample needs at least five open tasks, had ${openTasks.size}", openTasks.size >= 5)
        openTasks.take(5).forEach { composeRule.assertFullyVisible("task-${it.id}") }

        val file = composeRule.captureScreenshot("tasks-$mode")
        assertTrue("screenshot written to ${file.path}", file.length() > 0)
    }

    @Test
    fun rendersWorkspacePicker() {
        setContent()
        composeRule.onNodeWithTag("workspace-title").performClick()
        composeRule.waitForIdle()

        val file = composeRule.onNodeWithTag("workspace-switcher").writeWindowScreenshot("tasks-switcher-$mode")
        assertTrue("screenshot written to ${file.path}", file.length() > 0)
    }

    companion object {
        @JvmStatic
        @ParameterizedRobolectricTestRunner.Parameters(name = "dark={0}")
        fun parameters(): List<Array<Any>> = listOf(arrayOf(false), arrayOf(true))
    }
}