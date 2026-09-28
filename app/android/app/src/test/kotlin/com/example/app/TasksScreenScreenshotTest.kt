package com.example.app

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.DesignVariants
import com.example.app.feature.tasks.DesignVariantsProvider
import com.example.app.feature.tasks.TasksScreen
import com.example.app.feature.tasks.bucketLayoutVariants
import com.example.app.feature.tasks.sampleTasksUiState
import com.example.app.testing.captureScreenshot
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.ParameterizedRobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Renders every list layout variant with realistic data for visual comparison and checks the
 * density and collapsed-by-default requirements hold in each of them.
 */
@RunWith(ParameterizedRobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TasksScreenScreenshotTest(
    private val variants: DesignVariants,
    private val darkTheme: Boolean,
) {

    @get:Rule
    val composeRule = createComposeRule()

    private val firstOpenTaskIds = listOf("t1", "t2", "t3", "t4", "t5")

    @Test
    fun rendersVariantWithFiveVisibleRowsAndCollapsedDrafts() {
        var state by mutableStateOf(sampleTasksUiState())
        composeRule.setContent {
            DesignVariantsProvider(initial = variants) {
                AppTheme(darkTheme = darkTheme) {
                    TasksScreen(
                        state = state,
                        onRefresh = {},
                        onRetryTasks = {},
                        onSearchQueryChanged = {},
                        onSearchActiveChanged = {},
                        onToggleDrafts = { state = state.copy(draftsExpanded = !state.draftsExpanded) },
                        onToggleDone = { state = state.copy(doneExpanded = !state.doneExpanded) },
                        onTaskClick = {},
                        onScanDifferentCode = {},
                    )
                }
            }
        }

        firstOpenTaskIds.forEach { composeRule.assertFullyVisible("task-$it") }
        composeRule.onNodeWithTag("task-t8").assertDoesNotExist()
        composeRule.onNodeWithTag("task-t10").assertDoesNotExist()
        val collapsed = composeRule.captureScreenshot(screenshotName())

        scrollListToIfNeeded("drafts-toggle")
        composeRule.onNodeWithTag("drafts-toggle").performClick()
        scrollListToIfNeeded("task-t8")
        composeRule.onNodeWithTag("task-t8").assertIsDisplayed()
        val drafts = composeRule.captureScreenshot("${screenshotName()}-drafts")

        assertTrue("screenshot written to ${collapsed.path}", collapsed.length() > 0)
        assertTrue("screenshot written to ${drafts.path}", drafts.length() > 0)
    }

    /** Bottom-access variants place Drafts/Done inside the list, past the open tasks. */
    private fun scrollListToIfNeeded(tag: String) {
        val visible = composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()
        if (!visible) {
            composeRule.onNodeWithTag("task-list").performScrollToNode(hasTestTag(tag))
        }
    }

    private fun screenshotName(): String {
        val layout = variants.bucketPresentation.name.lowercase()
        val access = variants.collapsedAccess.name.lowercase()
        val mode = if (darkTheme) "dark" else "light"
        return "tasks-$layout-$access-$mode"
    }

    companion object {
        @JvmStatic
        @ParameterizedRobolectricTestRunner.Parameters(name = "{0} dark={1}")
        fun parameters(): List<Array<Any>> =
            bucketLayoutVariants.flatMap { variants ->
                listOf(false, true).map { darkTheme -> arrayOf<Any>(variants, darkTheme) }
            }
    }
}