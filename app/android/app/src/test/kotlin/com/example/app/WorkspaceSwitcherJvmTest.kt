package com.example.app

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.example.app.core.remote.Workspace
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.TasksScreen
import com.example.app.feature.tasks.TasksUiState
import com.example.app.feature.tasks.sampleTasks
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
class WorkspaceSwitcherJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val sidekick = Workspace(id = "ws-1", name = "Sidekick")
    private val android = Workspace(id = "ws-2", name = "Android companion")
    private val docs = Workspace(id = "ws-3", name = "Docs site")
    private val workspaces = listOf(sidekick, android, docs)

    private fun loadedState() = TasksUiState(
        workspaces = workspaces,
        isLoadingWorkspaces = false,
        currentWorkspace = sidekick,
        tasks = sampleTasks(),
    )

    /** Mirrors the ViewModel's transitions for the switcher-related callbacks. */
    private class Harness(initial: TasksUiState) {
        var state by mutableStateOf(initial)
        val selected = mutableListOf<String>()
        var workspaceRetries = 0
        var scans = 0
    }

    private fun setContent(harness: Harness) {
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                TasksScreen(
                    state = harness.state,
                    onRefresh = {},
                    onRetryTasks = {},
                    onRetryWorkspaces = { harness.workspaceRetries += 1 },
                    onWorkspaceSelected = { id ->
                        harness.selected += id
                        val picked = harness.state.workspaces.first { it.id == id }
                        harness.state = harness.state.copy(currentWorkspace = picked, isLoadingTasks = true)
                    },
                    onSwitcherQueryChanged = { harness.state = harness.state.copy(switcherQuery = it) },
                    onSearchQueryChanged = {},
                    onSearchActiveChanged = {},
                    onToggleDrafts = {},
                    onToggleDone = {},
                    onTaskClick = {},
                    onScanDifferentCode = { harness.scans += 1 },
                )
            }
        }
    }

    private fun openSwitcher() {
        composeRule.onNodeWithTag("workspace-title").performClick()
        composeRule.onNodeWithTag("workspace-filter").assertIsDisplayed()
    }

    @Test
    fun tappingTheTitleOpensTheSwitcherListingEveryWorkspaceWithTheCurrentOneSelected() {
        setContent(Harness(loadedState()))
        composeRule.onNodeWithTag("workspace-filter").assertDoesNotExist()

        openSwitcher()

        composeRule.onNodeWithTag("workspace-ws-1").assertIsDisplayed().assertIsSelected()
        composeRule.onNodeWithTag("workspace-ws-2").assertIsDisplayed().assertIsNotSelected()
        composeRule.onNodeWithTag("workspace-ws-3").assertIsDisplayed().assertIsNotSelected()
    }

    @Test
    fun typingFiltersWorkspacesCaseInsensitively() {
        setContent(Harness(loadedState()))
        openSwitcher()

        composeRule.onNodeWithTag("workspace-filter").performTextInput("ANDROID")

        composeRule.onNodeWithTag("workspace-ws-2").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-ws-1").assertDoesNotExist()
        composeRule.onNodeWithTag("workspace-ws-3").assertDoesNotExist()

        composeRule.onNodeWithTag("workspace-filter-clear").performClick()
        composeRule.onNodeWithTag("workspace-ws-1").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-ws-3").assertIsDisplayed()
    }

    @Test
    fun filterWithoutMatchesShowsMessage() {
        setContent(Harness(loadedState()))
        openSwitcher()

        composeRule.onNodeWithTag("workspace-filter").performTextInput("zzz")

        composeRule.onNodeWithTag("workspace-filter-empty").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-ws-1").assertDoesNotExist()
    }

    @Test
    fun tappingARowSelectsItClosesTheSwitcherAndKeepsTheNewTitleWhileTasksReload() {
        val harness = Harness(loadedState())
        setContent(harness)
        openSwitcher()

        composeRule.onNodeWithTag("workspace-ws-2").performClick()

        assertEquals(listOf("ws-2"), harness.selected)
        composeRule.onNodeWithTag("workspace-filter").assertDoesNotExist()
        composeRule.onNodeWithTag("tasks-loading").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-title").assertIsDisplayed().assertTextEquals("Android companion")
    }

    @Test
    fun closingAndReopeningTheSwitcherDropsTheFilter() {
        val harness = Harness(loadedState())
        setContent(harness)
        openSwitcher()
        composeRule.onNodeWithTag("workspace-filter").performTextInput("docs")
        composeRule.onNodeWithTag("workspace-ws-1").assertDoesNotExist()

        composeRule.onNodeWithTag("workspace-ws-3").performClick()
        openSwitcher()

        assertEquals("", harness.state.switcherQuery)
        composeRule.onNodeWithTag("workspace-ws-1").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-ws-3").assertIsSelected()
    }

    @Test
    fun scanEntryInvokesTheCallbackAndClosesTheSwitcher() {
        val harness = Harness(loadedState())
        setContent(harness)
        openSwitcher()

        composeRule.onNodeWithTag("scan-different-code").performClick()

        assertEquals(1, harness.scans)
        composeRule.onNodeWithTag("workspace-filter").assertDoesNotExist()
    }

    @Test
    fun loadingStateShowsProgressAndTheTitleNeverBlanks() {
        val loading = TasksUiState(isLoadingWorkspaces = true)
        setContent(Harness(loading))

        composeRule.onNodeWithTag("workspace-title").assertIsDisplayed().assertTextEquals("Workspaces…")
        openSwitcher()
        composeRule.onNodeWithTag("workspaces-loading").assertIsDisplayed()
    }

    @Test
    fun errorStateOffersRetry() {
        val harness = Harness(
            TasksUiState(isLoadingWorkspaces = false, workspacesError = "Workspaces could not be loaded."),
        )
        setContent(harness)
        openSwitcher()

        composeRule.onNodeWithTag("workspaces-error").assertTextEquals("Workspaces could not be loaded.")
        composeRule.onNodeWithTag("retry-workspaces").performClick()

        assertEquals(1, harness.workspaceRetries)
    }

    @Test
    fun emptyStateShowsMessage() {
        setContent(Harness(TasksUiState(isLoadingWorkspaces = false)))
        openSwitcher()

        composeRule.onNodeWithTag("empty-workspaces").assertIsDisplayed()
    }

    @Test
    fun titleShowsTheWorkspaceNameWithAChevron() {
        setContent(Harness(loadedState()))

        composeRule.onNodeWithTag("workspace-chevron", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-title").assertTextEquals("Sidekick")
    }
}