@file:OptIn(ExperimentalMaterial3Api::class)

package com.example.app.feature.tasks

import androidx.compose.foundation.background
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.tooling.preview.PreviewParameter
import androidx.compose.ui.tooling.preview.PreviewParameterProvider
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.example.app.core.remote.Task
import com.example.app.core.remote.Workspace
import com.example.app.core.ui.TaskStatusChip
import com.example.app.core.ui.relativeTime
import com.example.app.core.ui.theme.AppTheme
import com.example.app.core.ui.theme.LocalStyleVariant
import com.example.app.core.ui.theme.RowDividerStyle
import java.time.Instant
import kotlinx.coroutines.launch

private const val NOT_AVAILABLE_MESSAGE = "Not available yet"
private val ScreenHorizontalPadding = 16.dp

/**
 * Stateless Tasks landing screen. The [title] slot hosts the workspace name; the workspace
 * switcher plugs into it.
 */
@Composable
fun TasksScreen(
    state: TasksUiState,
    onRefresh: () -> Unit,
    onRetryTasks: () -> Unit,
    onSearchQueryChanged: (String) -> Unit,
    onSearchActiveChanged: (Boolean) -> Unit,
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
    onTaskClick: (Task) -> Unit,
    onScanDifferentCode: () -> Unit,
    modifier: Modifier = Modifier,
    snackbarHostState: SnackbarHostState = remember { SnackbarHostState() },
    title: @Composable () -> Unit = { WorkspaceTitlePlaceholder(state.currentWorkspace?.name) },
) {
    val scope = rememberCoroutineScope()
    val showNotAvailable: () -> Unit = {
        scope.launch { snackbarHostState.showSnackbar(NOT_AVAILABLE_MESSAGE) }
    }
    // Separate list states so leaving search returns to the bucketed scroll position.
    val bucketListState = rememberLazyListState()
    val searchListState = rememberLazyListState()

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
                    title = title,
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
                onToggleDrafts = onToggleDrafts,
                onToggleDone = onToggleDone,
                onTaskClick = onTaskClick,
            )
        }
    }
}

@Composable
internal fun WorkspaceTitlePlaceholder(name: String?) {
    Text(
        text = name ?: "Sidekick",
        style = MaterialTheme.typography.titleLarge,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        modifier = Modifier.testTag("workspace-title"),
    )
}

@Composable
private fun TasksTopBar(
    title: @Composable () -> Unit,
    onSearch: () -> Unit,
    onNewTask: () -> Unit,
    onScanDifferentCode: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    var variantSwitcherOpen by remember { mutableStateOf(false) }
    val variantsEditor = LocalDesignVariantsEditor.current

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
                    if (variantsEditor != null) {
                        DropdownMenuItem(
                            text = { Text("Design variants") },
                            onClick = {
                                menuOpen = false
                                variantSwitcherOpen = true
                            },
                            modifier = Modifier.testTag("design-variants"),
                        )
                    }
                }
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface),
    )

    if (variantSwitcherOpen && variantsEditor != null) {
        DesignVariantSwitcherDialog(
            current = LocalDesignVariants.current,
            onChange = variantsEditor,
            onDismiss = { variantSwitcherOpen = false },
        )
    }
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
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
    onTaskClick: (Task) -> Unit,
    modifier: Modifier = Modifier,
) {
    val error = state.tasksError
    val topChips = LocalDesignVariants.current.bucketPresentation == BucketPresentation.TOP_CHIPS
    when {
        state.isLoadingTasks -> CenteredContent(modifier) {
            CircularProgressIndicator(modifier = Modifier.testTag("tasks-loading"))
        }

        error != null -> CenteredContent(modifier) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text(
                    text = error,
                    style = MaterialTheme.typography.bodyMedium,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.testTag("tasks-error"),
                )
                Spacer(modifier = Modifier.height(12.dp))
                Button(onClick = onRetryTasks, modifier = Modifier.testTag("retry-tasks")) {
                    Text("Retry")
                }
            }
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

        topChips -> TopChipsList(
            state = state,
            listState = bucketListState,
            onToggleDrafts = onToggleDrafts,
            onToggleDone = onToggleDone,
            onTaskClick = onTaskClick,
            modifier = modifier,
        )

        else -> SectionedList(
            state = state,
            listState = bucketListState,
            onToggleDrafts = onToggleDrafts,
            onToggleDone = onToggleDone,
            onTaskClick = onTaskClick,
            modifier = modifier,
        )
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

@Composable
private fun TopChipsList(
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
    val chipsAtBottom = LocalDesignVariants.current.collapsedAccess == CollapsedAccess.BOTTOM
    val filterChips: @Composable () -> Unit = {
        ChipRow {
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
    }

    Column(modifier = modifier.fillMaxSize()) {
        if (!chipsAtBottom) {
            filterChips()
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
        if (chipsAtBottom) {
            HorizontalDivider(thickness = Dp.Hairline, color = MaterialTheme.colorScheme.outlineVariant)
            filterChips()
        }
    }
}

@Composable
private fun SectionedList(
    state: TasksUiState,
    listState: LazyListState,
    onToggleDrafts: () -> Unit,
    onToggleDone: () -> Unit,
    onTaskClick: (Task) -> Unit,
    modifier: Modifier = Modifier,
) {
    val variants = LocalDesignVariants.current
    val withHeaders = variants.bucketPresentation == BucketPresentation.HEADERS
    val accessAtTop = variants.collapsedAccess == CollapsedAccess.TOP
    val buckets = state.bucketed
    val openIsEmpty = buckets.needsAttention.isEmpty() && buckets.active.isEmpty()

    Column(modifier = modifier.fillMaxSize()) {
        if (accessAtTop) {
            ChipRow {
                CountChip(
                    label = "Drafts",
                    count = buckets.drafts.size,
                    selected = state.draftsExpanded,
                    onClick = onToggleDrafts,
                    tag = "drafts-toggle",
                )
                CountChip(
                    label = "Done",
                    count = buckets.done.size,
                    selected = state.doneExpanded,
                    onClick = onToggleDone,
                    tag = "done-toggle",
                )
            }
        }
        LazyColumn(
            state = listState,
            modifier = Modifier
                .fillMaxSize()
                .testTag("task-list"),
        ) {
            bucketSection("Needs attention", buckets.needsAttention, withHeaders, onTaskClick)
            bucketSection("Active", buckets.active, withHeaders, onTaskClick)
            if (openIsEmpty) {
                emptyBucketNote(BucketFilter.OPEN.emptyText)
            }
            if (accessAtTop) {
                if (state.draftsExpanded) {
                    bucketSection("Drafts", buckets.drafts, withHeaders, onTaskClick)
                }
                if (state.doneExpanded) {
                    bucketSection("Done", buckets.done, withHeaders, onTaskClick)
                }
            } else {
                collapsibleSection(
                    title = "Drafts",
                    tasks = buckets.drafts,
                    expanded = state.draftsExpanded,
                    onToggle = onToggleDrafts,
                    tag = "drafts-toggle",
                    onTaskClick = onTaskClick,
                )
                collapsibleSection(
                    title = "Done",
                    tasks = buckets.done,
                    expanded = state.doneExpanded,
                    onToggle = onToggleDone,
                    tag = "done-toggle",
                    onTaskClick = onTaskClick,
                )
            }
        }
    }
}

@Composable
private fun ChipRow(content: @Composable () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        content()
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

private fun LazyListScope.bucketSection(
    title: String,
    tasks: List<Task>,
    withHeader: Boolean,
    onTaskClick: (Task) -> Unit,
) {
    if (tasks.isEmpty()) {
        return
    }
    if (withHeader) {
        item(key = "header-$title") {
            Text(
                text = title,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier
                    .padding(
                        start = ScreenHorizontalPadding,
                        end = ScreenHorizontalPadding,
                        top = 12.dp,
                        bottom = 4.dp,
                    )
                    .testTag("section-header"),
            )
        }
    }
    taskRows(tasks, onTaskClick)
}

private fun LazyListScope.collapsibleSection(
    title: String,
    tasks: List<Task>,
    expanded: Boolean,
    onToggle: () -> Unit,
    tag: String,
    onTaskClick: (Task) -> Unit,
) {
    item(key = "toggle-$title") {
        CollapsibleHeader(
            title = title,
            count = tasks.size,
            expanded = expanded,
            onToggle = onToggle,
            tag = tag,
        )
    }
    if (expanded) {
        taskRows(tasks, onTaskClick)
    }
}

@Composable
private fun CollapsibleHeader(
    title: String,
    count: Int,
    expanded: Boolean,
    onToggle: () -> Unit,
    tag: String,
) {
    Column {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onToggle)
                .testTag(tag)
                .padding(horizontal = ScreenHorizontalPadding, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(modifier = Modifier.width(8.dp))
            Text(
                text = count.toString(),
                style = MaterialTheme.typography.labelSmall,
                modifier = Modifier
                    .background(MaterialTheme.colorScheme.surfaceContainer, RoundedCornerShape(10.dp))
                    .padding(horizontal = 7.dp, vertical = 1.dp),
            )
            Spacer(modifier = Modifier.weight(1f))
            Icon(
                imageVector = Icons.Default.KeyboardArrowDown,
                contentDescription = if (expanded) "Collapse $title" else "Expand $title",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.rotate(if (expanded) 180f else 0f),
            )
        }
        RowDivider()
    }
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
    val inset = when (LocalStyleVariant.current.rowDividerStyle) {
        RowDividerStyle.FULL_WIDTH -> 0.dp
        RowDividerStyle.INSET -> ScreenHorizontalPadding
    }
    HorizontalDivider(
        modifier = Modifier.padding(start = inset),
        thickness = Dp.Hairline,
        color = MaterialTheme.colorScheme.outlineVariant,
    )
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
    workspaces = listOf(SampleWorkspace),
    isLoadingWorkspaces = false,
    currentWorkspace = SampleWorkspace,
    tasks = sampleTasks(now),
)

private class BucketLayoutPreviewProvider : PreviewParameterProvider<DesignVariants> {
    override val values: Sequence<DesignVariants> = bucketLayoutVariants.asSequence()
}

@Composable
private fun TasksScreenPreview(variants: DesignVariants, darkTheme: Boolean) {
    DesignVariantsProvider(initial = variants) {
        AppTheme(darkTheme = darkTheme) {
            TasksScreen(
                state = sampleTasksUiState(),
                onRefresh = {},
                onRetryTasks = {},
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

@Preview(name = "Tasks light", showBackground = true, widthDp = 360, heightDp = 640)
@Composable
private fun TasksScreenLightPreview(
    @PreviewParameter(BucketLayoutPreviewProvider::class) variants: DesignVariants,
) {
    TasksScreenPreview(variants = variants, darkTheme = false)
}

@Preview(name = "Tasks dark", showBackground = true, widthDp = 360, heightDp = 640)
@Composable
private fun TasksScreenDarkPreview(
    @PreviewParameter(BucketLayoutPreviewProvider::class) variants: DesignVariants,
) {
    TasksScreenPreview(variants = variants, darkTheme = true)
}