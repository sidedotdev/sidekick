package com.example.app

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.DesignVariants
import com.example.app.feature.tasks.DesignVariantsProvider
import com.example.app.feature.tasks.TasksScreen
import com.example.app.feature.tasks.sampleTasksUiState
import com.example.app.feature.tasks.switcherVariants
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
 * Renders the closed title and the open switcher for every affordance × container pairing.
 * The switcher lives in its own window, so its window is captured separately from the main screen.
 */
@RunWith(ParameterizedRobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class WorkspaceSwitcherScreenshotTest(
    private val variants: DesignVariants,
    private val darkTheme: Boolean,
) {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun rendersTitleAndOpenSwitcher() {
        composeRule.setContent {
            DesignVariantsProvider(initial = variants) {
                AppTheme(darkTheme = darkTheme) {
                    TasksScreen(
                        state = sampleTasksUiState(),
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

        val closed = composeRule.captureScreenshot("${screenshotName()}-title")

        composeRule.onNodeWithTag("workspace-title").performClick()
        composeRule.onNodeWithTag("workspace-filter").assertIsDisplayed()
        composeRule.waitForIdle()
        val open = composeRule.onNodeWithTag("workspace-switcher").writeWindowScreenshot("${screenshotName()}-open")

        assertTrue("screenshot written to ${closed.path}", closed.length() > 0)
        assertTrue("screenshot written to ${open.path}", open.length() > 0)
    }

    private fun screenshotName(): String {
        val affordance = variants.workspaceAffordance.name.lowercase()
        val container = variants.switcherContainer.name.lowercase()
        val mode = if (darkTheme) "dark" else "light"
        return "switcher-$affordance-$container-$mode"
    }

    companion object {
        @JvmStatic
        @ParameterizedRobolectricTestRunner.Parameters(name = "{0} dark={1}")
        fun parameters(): List<Array<Any>> =
            switcherVariants.flatMap { variants ->
                listOf(false, true).map { darkTheme -> arrayOf<Any>(variants, darkTheme) }
            }
    }
}