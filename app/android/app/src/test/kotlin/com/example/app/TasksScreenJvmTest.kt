package com.example.app

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.height
import com.example.app.core.remote.Task
import com.example.app.core.remote.Workspace
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.TasksScreen
import com.example.app.feature.tasks.TasksUiState
import com.example.app.feature.tasks.sampleTasks
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
class TasksScreenJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val workspace = Workspace(id = "ws-1", name = "Sidekick")

    private fun task(id: String, title: String, status: String, updated: String, description: String = "") =
        Task(
            id = id,
            workspaceId = workspace.id,
            title = title,
            description = description,
            status = status,
            updated = updated,
        )

    private val tasks = listOf(
        task("blocked-1", "Blocked pairing fix", "blocked", "2026-09-01T10:00:00Z"),
        task("review-1", "Review switcher", "in_review", "2026-09-02T10:00:00Z"),
        task("progress-1", "Pull to refresh", "in_progress", "2026-09-03T10:00:00Z", "Use PullToRefreshBox."),
        task("todo-1", "Relative times", "to_do", "2026-09-01T09:00:00Z"),
        task("todo-2", "Dark mode pass", "to_do", "2026-08-30T09:00:00Z"),
        task("draft-1", "Draft pairing notes", "drafting", "2026-09-05T10:00:00Z"),
        task("done-1", "Finished pairing flow", "complete", "2026-09-06T10:00:00Z"),
    )

    private val openTaskIds = listOf("review-1", "blocked-1", "progress-1", "todo-1", "todo-2")

    private fun loadedState() = TasksUiState(
        workspaces = listOf(workspace),
        isLoadingWorkspaces = false,
        currentWorkspace = workspace,
        tasks = tasks,
    )

    /** Mirrors the ViewModel's state transitions so the screen can be driven end to end. */
    private class Harness(initial: TasksUiState) {
        var state by mutableStateOf(initial)
        val clicked = mutableListOf<Task>()
        var retries = 0
        var scans = 0
    }

    @Composable
    private fun TestTasksScreen(harness: Harness) {
        AppTheme(darkTheme = false) {
            TasksScreen(
                state = harness.state,
                onRefresh = {},
                onRetryTasks = { harness.retries += 1 },
                onRetryWorkspaces = {},
                onWorkspaceSelected = {},
                onSwitcherQueryChanged = { harness.state = harness.state.copy(switcherQuery = it) },
                onSearchQueryChanged = { harness.state = harness.state.copy(searchQuery = it) },
                onSearchActiveChanged = { active ->
                    harness.state = harness.state.copy(
                        isSearchActive = active,
                        searchQuery = if (active) harness.state.searchQuery else "",
                    )
                },
                onToggleDrafts = {
                    harness.state = harness.state.copy(draftsExpanded = !harness.state.draftsExpanded)
                },
                onToggleDone = {
                    harness.state = harness.state.copy(doneExpanded = !harness.state.doneExpanded)
                },
                onTaskClick = { harness.clicked += it },
                onScanDifferentCode = { harness.scans += 1 },
            )
        }
    }

    private fun setContent(harness: Harness) {
        composeRule.setContent { TestTasksScreen(harness) }
    }

    @Test
    fun atLeastFiveRowsAreFullyVisibleWithoutScrolling() {
        setContent(Harness(loadedState()))

        openTaskIds.forEach { composeRule.assertFullyVisible("task-$it") }
    }

    @Test
    fun openTasksAreOrderedByBucketThenUpdatedAndShowRowDetails() {
        setContent(Harness(loadedState()))

        val tops = openTaskIds.map { composeRule.onNodeWithTag("task-$it").getBoundsInRoot().top }
        assertEquals(tops.sorted(), tops)
        composeRule.onNodeWithTag("task-progress-1-status").assertTextEquals("In progress")
        composeRule.onNodeWithTag("task-progress-1-description", useUnmergedTree = true)
            .assertTextEquals("Use PullToRefreshBox.")
        composeRule.onNodeWithTag("task-draft-1").assertDoesNotExist()
        composeRule.onNodeWithTag("task-done-1").assertDoesNotExist()
    }

    @Test
    fun draftsAndDoneAreHiddenUntilToggledOnce() {
        setContent(Harness(loadedState()))

        composeRule.onNodeWithTag("drafts-toggle").assertIsDisplayed().performClick()
        composeRule.onNodeWithTag("task-draft-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-done-1").assertDoesNotExist()

        composeRule.onNodeWithTag("done-toggle").performClick()
        composeRule.onNodeWithTag("task-done-1").assertIsDisplayed()
    }

    @Test
    fun searchFlattensEveryBucketWithDraftsAndDoneLastAndClosingRestoresBuckets() {
        setContent(Harness(loadedState()))

        composeRule.onNodeWithTag("search-open").performClick()
        composeRule.onNodeWithTag("task-search").performTextInput("pairing")

        composeRule.onNodeWithTag("task-blocked-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-draft-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-done-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-review-1").assertDoesNotExist()
        val tops = listOf("blocked-1", "draft-1", "done-1")
            .map { composeRule.onNodeWithTag("task-$it").getBoundsInRoot().top }
        assertEquals(tops.sorted(), tops)
        composeRule.onNodeWithTag("search-count").assertIsDisplayed()

        composeRule.onNodeWithTag("search-close").performClick()
        composeRule.onNodeWithTag("task-review-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-draft-1").assertDoesNotExist()
        composeRule.onNodeWithTag("task-search").assertDoesNotExist()
    }

    @Test
    fun clearingTheQueryRestoresBucketsWhileSearchStaysOpen() {
        setContent(Harness(loadedState()))

        composeRule.onNodeWithTag("search-open").performClick()
        composeRule.onNodeWithTag("task-search").performTextInput("draft")
        composeRule.onNodeWithTag("task-draft-1").assertIsDisplayed()

        composeRule.onNodeWithTag("search-clear").performClick()
        composeRule.onNodeWithTag("task-search").assertIsDisplayed()
        composeRule.onNodeWithTag("task-draft-1").assertDoesNotExist()
        composeRule.onNodeWithTag("task-review-1").assertIsDisplayed()
    }

    @Test
    fun filterChipsSitAboveTheListAndShowOneBucketAtATime() {
        setContent(Harness(loadedState()))

        val listTop = composeRule.onNodeWithTag("task-list").getUnclippedBoundsInRoot().top
        val chipsBottom = composeRule.onNodeWithTag("drafts-toggle").getUnclippedBoundsInRoot().bottom
        assertTrue("filter chips ending at $chipsBottom should be above the list starting at $listTop", chipsBottom <= listTop)
        composeRule.onNodeWithText("Needs attention").assertDoesNotExist()
        composeRule.onNodeWithText("Active").assertDoesNotExist()
        composeRule.onNodeWithTag("task-draft-1").assertDoesNotExist()

        composeRule.onNodeWithTag("drafts-toggle").performClick()
        composeRule.onNodeWithTag("task-draft-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-review-1").assertDoesNotExist()

        composeRule.onNodeWithTag("done-toggle").performClick()
        composeRule.onNodeWithTag("task-done-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-draft-1").assertDoesNotExist()

        composeRule.onNodeWithTag("open-filter").performClick()
        composeRule.onNodeWithTag("task-review-1").assertIsDisplayed()
        composeRule.onNodeWithTag("task-done-1").assertDoesNotExist()
    }

    @Test
    fun closingSearchRestoresTheBucketedScrollPosition() {
        val manyOpenTasks = (1..30).map { n ->
            task("open-$n", "Open task $n", "to_do", "2026-09-%02dT10:00:00Z".format(31 - n))
        }
        setContent(Harness(loadedState().copy(tasks = manyOpenTasks + tasks)))
        composeRule.onNodeWithTag("task-list").performScrollToNode(hasTestTag("task-open-30"))
        val scrolledTop = composeRule.onNodeWithTag("task-open-30").getBoundsInRoot().top
        composeRule.onNodeWithTag("task-open-1").assertIsNotDisplayed()

        composeRule.onNodeWithTag("search-open").performClick()
        composeRule.onNodeWithTag("task-search").performTextInput("finished")
        composeRule.onNodeWithTag("task-done-1").assertIsDisplayed()
        composeRule.onNodeWithTag("search-close").performClick()

        composeRule.onNodeWithTag("task-open-1").assertIsNotDisplayed()
        assertEquals(scrolledTop, composeRule.onNodeWithTag("task-open-30").getBoundsInRoot().top)
    }

    @Test
    fun searchWithoutMatchesShowsMessage() {
        setContent(Harness(loadedState()))

        composeRule.onNodeWithTag("search-open").performClick()
        composeRule.onNodeWithTag("task-search").performTextInput("zzz")

        composeRule.onNodeWithTag("search-empty").assertIsDisplayed()
        composeRule.onNodeWithTag("task-list").assertDoesNotExist()
    }

    @Test
    fun tappingARowReportsTheTask() {
        val harness = Harness(loadedState())
        setContent(harness)

        composeRule.onNodeWithTag("task-progress-1").performClick()

        assertEquals(listOf("progress-1"), harness.clicked.map { it.id })
    }

    @Test
    fun newTaskShowsNotAvailableSnackbar() {
        setContent(Harness(loadedState()))

        composeRule.onNodeWithTag("new-task").performClick()

        composeRule.onNodeWithText("Not available yet").assertIsDisplayed()
    }

    @Test
    fun overflowMenuOffersScanningADifferentCode() {
        val harness = Harness(loadedState())
        setContent(harness)

        composeRule.onNodeWithTag("overflow-menu").performClick()
        composeRule.onNodeWithTag("scan-different-code").performClick()

        assertEquals(1, harness.scans)
    }

    @Test
    fun loadingStateShowsProgressAndKeepsTheTitle() {
        setContent(Harness(loadedState().copy(isLoadingTasks = true, tasks = emptyList())))

        composeRule.onNodeWithTag("tasks-loading").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-title").assertTextEquals("Sidekick")
    }

    @Test
    fun errorStateOffersRetry() {
        val harness = Harness(loadedState().copy(tasks = emptyList(), tasksError = "Tasks could not be loaded."))
        setContent(harness)

        composeRule.onNodeWithTag("tasks-error").assertTextEquals("Tasks could not be loaded.")
        composeRule.onNodeWithTag("retry-tasks").performClick()

        assertEquals(1, harness.retries)
    }

    @Test
    fun emptyStateShowsMessage() {
        setContent(Harness(loadedState().copy(tasks = emptyList())))

        composeRule.onNodeWithTag("empty-tasks").assertIsDisplayed()
        composeRule.onNodeWithTag("task-list").assertDoesNotExist()
    }
}

/**
 * Asserts the whole row lies inside the list viewport. Unclipped bounds are used because
 * clipped bounds shrink to fit the viewport and would pass for partially visible rows.
 */
internal fun ComposeContentTestRule.assertFullyVisible(tag: String) {
    val viewport = onNodeWithTag("task-list").getBoundsInRoot()
    val bounds = onNodeWithTag(tag).assertIsDisplayed().getUnclippedBoundsInRoot()
    assertTrue(
        "$tag at $bounds is not fully inside the list viewport $viewport",
        bounds.height > Dp(0f) &&
            bounds.top >= viewport.top && bounds.bottom <= viewport.bottom &&
            bounds.left >= viewport.left && bounds.right <= viewport.right,
    )
}