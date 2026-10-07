@file:OptIn(ExperimentalMaterial3Api::class)

package com.example.app.feature.tasks

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.example.app.core.remote.Task
import com.example.app.core.remote.Workspace
import com.example.app.core.ui.TaskStatusChip
import com.example.app.core.ui.relativeTime
import com.example.app.core.ui.theme.AppTheme
import java.time.Instant
import kotlinx.coroutines.launch

private const val NOT_AVAILABLE_MESSAGE = "Not available yet"
private val ScreenHorizontalPadding = 16.dp

/** Stateless Tasks landing screen; the top bar title doubles as the workspace switcher. */
@Composable
fun TasksScreen(
    state: TasksUiState,
    onRefresh: () -> Unit,
    onRetryTasks: () -> Unit,
    onRetryWorkspaces: () -> Unit,
    onWorkspaceSelected: (String) -> Unit,
    onSwitcherQueryChanged: (String) -> Unit,
    onSearchQueryChanged: (String) -> Unit,
    onSearchActiveChanged: (Boolean) -> Unit,
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
    onTaskClick: (Task) -> Unit,
    onScanDifferentCode: () -> Unit,
    modifier: Modifier = Modifier,
    snackbarHostState: SnackbarHostState = remember { SnackbarHostState() },
) {
    val scope = rememberCoroutineScope()
    val showNotAvailable: () -> Unit = {
        scope.launch { snackbarHostState.showSnackbar(NOT_AVAILABLE_MESSAGE) }
    }
    // Separate list states so leaving search returns to the bucketed scroll position.
    val bucketListState = rememberLazyListState()
    val searchListState = rememberLazyListState()
    var switcherOpen by rememberSaveable { mutableStateOf(false) }
    // Reopening starts from the full list rather than a stale filter.
    val closeSwitcher: () -> Unit = {
        switcherOpen = false
        onSwitcherQueryChanged("")
    }

    Scaffold(
        modifier = modifier,
        topBar = {
            if (state.isSearchActive) {
                SearchTopBar(
                    query = state.searchQuery,
                    onQueryChanged = onSearchQueryChanged,
                    onClose = { onSearchActiveChanged(false) },
                )
            } else {
                TasksTopBar(
                    title = {
                        Box {
                            WorkspaceTitle(state = state, onClick = { switcherOpen = true })
                            if (switcherOpen) {
                                WorkspaceSwitcher(
                                    state = state,
                                    onQueryChanged = onSwitcherQueryChanged,
                                    onSelect = { id ->
                                        closeSwitcher()
                                        onWorkspaceSelected(id)
                                    },
                                    onRetry = onRetryWorkspaces,
                                    onScanDifferentCode = {
                                        closeSwitcher()
                                        onScanDifferentCode()
                                    },
                                    onDismiss = closeSwitcher,
                                )
                            }
                        }
                    },
                    onSearch = { onSearchActiveChanged(true) },
                    onNewTask = showNotAvailable,
                    onScanDifferentCode = onScanDifferentCode,
                )
            }
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = state.isRefreshing,
            onRefresh = onRefresh,
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            TasksBody(
                state = state,
                bucketListState = bucketListState,
                searchListState = searchListState,
                onRetryTasks = onRetryTasks,
                onRetryWorkspaces = onRetryWorkspaces,
                onToggleDrafts = onToggleDrafts,
                onToggleDone = onToggleDone,
                onTaskClick = onTaskClick,
            )
        }
    }
}

@Composable
private fun TasksTopBar(
    title: @Composable () -> Unit,
    onSearch: () -> Unit,
    onNewTask: () -> Unit,
    onScanDifferentCode: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }

    TopAppBar(
        title = title,
        actions = {
            IconButton(onClick = onSearch, modifier = Modifier.testTag("search-open")) {
                Icon(Icons.Default.Search, contentDescription = "Search tasks")
            }
            IconButton(onClick = onNewTask, modifier = Modifier.testTag("new-task")) {
                Icon(Icons.Default.Add, contentDescription = "New task")
            }
            Box {
                IconButton(onClick = { menuOpen = true }, modifier = Modifier.testTag("overflow-menu")) {
                    Icon(Icons.Default.MoreVert, contentDescription = "More options")
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    DropdownMenuItem(
                        text = { Text("Scan a different pairing code") },
                        onClick = {
                            menuOpen = false
                            onScanDifferentCode()
                        },
                        modifier = Modifier.testTag("scan-different-code"),
                    )
                }
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface),
    )
}

@Composable
private fun SearchTopBar(
    query: String,
    onQueryChanged: (String) -> Unit,
    onClose: () -> Unit,
) {
    val focusRequester = remember { FocusRequester() }
    TopAppBar(
        navigationIcon = {
            IconButton(onClick = onClose, modifier = Modifier.testTag("search-close")) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Close search")
            }
        },
        title = {
            TextField(
                value = query,
                onValueChange = onQueryChanged,
                modifier = Modifier
                    .fillMaxWidth()
                    .focusRequester(focusRequester)
                    .testTag("task-search"),
                placeholder = { Text("Search tasks") },
                singleLine = true,
                trailingIcon = {
                    if (query.isNotEmpty()) {
                        IconButton(
                            onClick = { onQueryChanged("") },
                            modifier = Modifier.testTag("search-clear"),
                        ) {
                            Icon(Icons.Default.Close, contentDescription = "Clear search")
                        }
                    }
                },
                colors = TextFieldDefaults.colors(
                    focusedContainerColor = Color.Transparent,
                    unfocusedContainerColor = Color.Transparent,
                    focusedIndicatorColor = Color.Transparent,
                    unfocusedIndicatorColor = Color.Transparent,
                ),
            )
        },
        colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface),
    )
    LaunchedEffect(Unit) { focusRequester.requestFocus() }
}

@Composable
private fun TasksBody(
    state: TasksUiState,
    bucketListState: LazyListState,
    searchListState: LazyListState,
    onRetryTasks: () -> Unit,
    onRetryWorkspaces: () -> Unit,
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
    onTaskClick: (Task) -> Unit,
    modifier: Modifier = Modifier,
) {
    val error = state.tasksError
    val workspacesError = state.workspacesError
    when {
        state.isLoadingTasks -> CenteredContent(modifier) {
            CircularProgressIndicator(modifier = Modifier.testTag("tasks-loading"))
        }

        // Without a workspace there are no tasks to fail on, so the workspace
        // failure is the one worth showing and retrying from the main screen.
        workspacesError != null && state.currentWorkspace == null -> CenteredContent(modifier) {
            LoadFailure(
                message = workspacesError,
                detail = state.workspacesErrorDetail,
                onRetry = onRetryWorkspaces,
                messageTag = "tasks-error",
                retryTag = "retry-workspaces-from-tasks",
            )
        }

        error != null -> CenteredContent(modifier) {
            LoadFailure(
                message = error,
                detail = state.tasksErrorDetail,
                onRetry = onRetryTasks,
                messageTag = "tasks-error",
                retryTag = "retry-tasks",
            )
        }

        state.isSearchActive && state.searchQuery.isNotBlank() -> SearchResults(
            query = state.searchQuery,
            results = state.searchResults,
            listState = searchListState,
            onTaskClick = onTaskClick,
            modifier = modifier,
        )

        state.tasks.isEmpty() -> CenteredContent(modifier) {
            Text(
                text = "No tasks in this workspace yet.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                modifier = Modifier.testTag("empty-tasks"),
            )
        }

        else -> BucketFilterList(
            state = state,
            listState = bucketListState,
            onToggleDrafts = onToggleDrafts,
            onToggleDone = onToggleDone,
            onTaskClick = onTaskClick,
            modifier = modifier,
        )
    }
}

@Composable
private fun LoadFailure(
    message: String,
    detail: String?,
    onRetry: () -> Unit,
    messageTag: String,
    retryTag: String,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier.padding(horizontal = 24.dp),
    ) {
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            textAlign = TextAlign.Center,
            modifier = Modifier.testTag(messageTag),
        )
        if (detail != null) {
            Spacer(modifier = Modifier.height(8.dp))
            Text(
                text = detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                modifier = Modifier.testTag("tasks-error-detail"),
            )
        }
        Spacer(modifier = Modifier.height(12.dp))
        Button(onClick = onRetry, modifier = Modifier.testTag(retryTag)) {
            Text("Retry")
        }
    }
}

/** Fills the viewport with centred content while staying scrollable so pull-to-refresh works. */
@Composable
private fun CenteredContent(
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    LazyColumn(modifier = modifier.fillMaxSize()) {
        item {
            Box(
                modifier = Modifier
                    .fillParentMaxSize()
                    .padding(24.dp),
                contentAlignment = Alignment.Center,
            ) {
                content()
            }
        }
    }
}

@Composable
private fun SearchResults(
    query: String,
    results: List<Task>,
    listState: LazyListState,
    onTaskClick: (Task) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (results.isEmpty()) {
        CenteredContent(modifier) {
            Text(
                text = "No tasks match \u201c${query.trim()}\u201d",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                modifier = Modifier.testTag("search-empty"),
            )
        }
        return
    }
    val downRanked = results.any { bucketFor(it.status) >= TaskBucket.DRAFTS }
    val note = buildString {
        append(results.size)
        append(if (results.size == 1) " result" else " results")
        if (downRanked) append(" \u00b7 drafts and done ranked last")
    }
    LazyColumn(
        state = listState,
        modifier = modifier
            .fillMaxSize()
            .testTag("task-list"),
    ) {
        item(key = "search-count") {
            Text(
                text = note,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier
                    .padding(horizontal = ScreenHorizontalPadding, vertical = 8.dp)
                    .testTag("search-count"),
            )
        }
        taskRows(results, onTaskClick)
    }
}

private enum class BucketFilter(val emptyText: String) {
    OPEN("No open tasks"),
    DRAFTS("No drafts"),
    DONE("Nothing finished yet")
}

private fun TasksUiState.selectedBucketFilter(): BucketFilter = when {
    draftsExpanded -> BucketFilter.DRAFTS
    doneExpanded -> BucketFilter.DONE
    else -> BucketFilter.OPEN
}

/**
 * Single-select chips over the two independent expansion toggles: flips whichever toggle
 * disagrees with the requested filter so exactly one group is shown.
 */
private fun TasksUiState.selectBucketFilter(
    filter: BucketFilter,
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
) {
    val wantDrafts = filter == BucketFilter.DRAFTS
    val wantDone = filter == BucketFilter.DONE
    if (draftsExpanded != wantDrafts) {
        onToggleDrafts()
    }
    if (doneExpanded != wantDone) {
        onToggleDone()
    }
}

/**
 * Open / Drafts / Done filter chips above the list. Open lists the Needs-attention bucket
 * followed by Active with no headers; ordering and status dots carry the grouping.
 */
@Composable
private fun BucketFilterList(
    state: TasksUiState,
    listState: LazyListState,
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
    onTaskClick: (Task) -> Unit,
    modifier: Modifier = Modifier,
) {
    val buckets = state.bucketed
    val openTasks = buckets.needsAttention + buckets.active
    val selected = state.selectedBucketFilter()
    val shown = when (selected) {
        BucketFilter.OPEN -> openTasks
        BucketFilter.DRAFTS -> buckets.drafts
        BucketFilter.DONE -> buckets.done
    }

    Column(modifier = modifier.fillMaxSize()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 12.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            CountChip(
                label = "Open",
                count = openTasks.size,
                selected = selected == BucketFilter.OPEN,
                onClick = { state.selectBucketFilter(BucketFilter.OPEN, onToggleDrafts, onToggleDone) },
                tag = "open-filter",
            )
            CountChip(
                label = "Drafts",
                count = buckets.drafts.size,
                selected = selected == BucketFilter.DRAFTS,
                onClick = { state.selectBucketFilter(BucketFilter.DRAFTS, onToggleDrafts, onToggleDone) },
                tag = "drafts-toggle",
            )
            CountChip(
                label = "Done",
                count = buckets.done.size,
                selected = selected == BucketFilter.DONE,
                onClick = { state.selectBucketFilter(BucketFilter.DONE, onToggleDrafts, onToggleDone) },
                tag = "done-toggle",
            )
        }
        LazyColumn(
            state = listState,
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .testTag("task-list"),
        ) {
            if (shown.isEmpty()) {
                emptyBucketNote(selected.emptyText)
            } else {
                taskRows(shown, onTaskClick)
            }
        }
    }
}

@Composable
private fun CountChip(
    label: String,
    count: Int,
    selected: Boolean,
    onClick: () -> Unit,
    tag: String,
) {
    FilterChip(
        selected = selected,
        onClick = onClick,
        label = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(label)
                Spacer(modifier = Modifier.width(6.dp))
                Text(
                    text = count.toString(),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        modifier = Modifier.testTag(tag),
    )
}

private fun LazyListScope.emptyBucketNote(text: String) {
    item(key = "empty-bucket") {
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier
                .padding(horizontal = ScreenHorizontalPadding, vertical = 16.dp)
                .testTag("bucket-empty"),
        )
    }
}

private fun LazyListScope.taskRows(tasks: List<Task>, onTaskClick: (Task) -> Unit) {
    items(items = tasks, key = { task -> task.id }) { task ->
        TaskRow(task = task, onClick = { onTaskClick(task) })
    }
}

/** Compact two-line row: title + relative time, then status indicator + description preview. */
@Composable
internal fun TaskRow(
    task: Task,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth()) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onClick)
                .testTag("task-" + task.id)
                .padding(horizontal = ScreenHorizontalPadding, vertical = 10.dp),
        ) {
            TaskRowTitleLine(task)
            Spacer(modifier = Modifier.height(4.dp))
            TaskRowStatusLine(task)
        }
        RowDivider()
    }
}

@Composable
private fun TaskRowTitleLine(task: Task) {
    Row(verticalAlignment = Alignment.Top) {
        Text(
            text = task.title,
            style = MaterialTheme.typography.titleMedium,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        val time = relativeTime(task.updated)
        if (time.isNotEmpty()) {
            Spacer(modifier = Modifier.width(8.dp))
            Text(
                text = time,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
    }
}

@Composable
private fun TaskRowStatusLine(task: Task) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        TaskStatusChip(
            status = task.status,
            modifier = Modifier.testTag("task-" + task.id + "-status"),
        )
        if (task.description.isNotBlank()) {
            Spacer(modifier = Modifier.width(10.dp))
            Text(
                text = task.description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .weight(1f)
                    .testTag("task-" + task.id + "-description"),
            )
        }
    }
}

@Composable
private fun RowDivider() {
    HorizontalDivider(thickness = Dp.Hairline, color = MaterialTheme.colorScheme.outlineVariant)
}

private val SampleWorkspace = Workspace(id = "ws-sidekick", name = "Sidekick")

private fun sampleTask(
    id: String,
    title: String,
    status: String,
    updated: Instant,
    description: String = "",
) = Task(
    id = id,
    workspaceId = SampleWorkspace.id,
    title = title,
    description = description,
    status = status,
    updated = updated.toString(),
)

/** Realistic data for previews and screenshot tests; timestamps are relative to [now]. */
internal fun sampleTasks(now: Instant = Instant.now()): List<Task> {
    fun minutesAgo(minutes: Long): Instant = now.minusSeconds(minutes * 60)
    fun daysAgo(days: Long): Instant = minutesAgo(days * 24 * 60)
    return listOf(
        sampleTask("t1", "Fix flaky pairing test on CI", "blocked", minutesAgo(12), "Robolectric run times out on the QR scan step."),
        sampleTask("t2", "Review workspace switcher PR", "in_review", minutesAgo(45)),
        sampleTask("t3", "Add pull-to-refresh to the task list", "in_progress", minutesAgo(3 * 60), "Use PullToRefreshBox from Material 3."),
        sampleTask("t4", "Persist the last selected workspace", "in_progress", minutesAgo(5 * 60), "DataStore key alongside pairing credentials."),
        sampleTask("t5", "Relative time labels for updated timestamps", "to_do", minutesAgo(26 * 60)),
        sampleTask("t6", "Dark mode pass over status colours", "to_do", daysAgo(2), "Check contrast of amber and green on near-black."),
        sampleTask("t7", "Task detail placeholder route", "to_do", daysAgo(3)),
        sampleTask("t8", "Explore a bottom filter bar", "drafting", daysAgo(4)),
        sampleTask("t9", "Notes on Slack-style density", "drafting", daysAgo(6), "Row heights, padding, dividers."),
        sampleTask("t10", "Replace the default purple palette", "complete", daysAgo(8)),
        sampleTask("t11", "Upgrade the Compose BOM", "complete", daysAgo(9)),
        sampleTask("t12", "Spike: server-side search", "canceled", daysAgo(20)),
    )
}

internal fun sampleTasksUiState(now: Instant = Instant.now()): TasksUiState = TasksUiState(
    workspaces = sampleWorkspaces(),
    isLoadingWorkspaces = false,
    currentWorkspace = SampleWorkspace,
    tasks = sampleTasks(now),
)

@Composable
private fun TasksScreenPreview(darkTheme: Boolean) {
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

@Preview(name = "Tasks light", showBackground = true, widthDp = 360, heightDp = 640)
@Composable
private fun TasksScreenLightPreview() {
    TasksScreenPreview(darkTheme = false)
}

@Preview(name = "Tasks dark", showBackground = true, widthDp = 360, heightDp = 640)
@Composable
private fun TasksScreenDarkPreview() {
    TasksScreenPreview(darkTheme = true)
}
