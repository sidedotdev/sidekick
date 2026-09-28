@file:OptIn(ExperimentalMaterial3Api::class)

package com.example.app.feature.tasks

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.tooling.preview.PreviewParameter
import androidx.compose.ui.tooling.preview.PreviewParameterProvider
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.example.app.core.remote.Workspace
import com.example.app.core.ui.theme.AppTheme

private val SwitcherHorizontalPadding = 16.dp

/**
 * Top bar title that opens the workspace switcher. The label never blanks: while workspaces are
 * still loading it reads "Workspaces…" so the bar keeps its shape.
 */
@Composable
fun WorkspaceTitle(
    state: TasksUiState,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val name = state.currentWorkspace?.name
    val label = name ?: if (state.isLoadingWorkspaces) "Workspaces…" else "Workspaces"
    val affordance = LocalDesignVariants.current.workspaceAffordance
    Row(
        modifier = modifier
            .testTag("workspace-title")
            .clip(RoundedCornerShape(8.dp))
            .clickable(onClick = onClick, role = Role.Button)
            .padding(horizontal = 4.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        if (affordance == WorkspaceAffordance.AVATAR) {
            WorkspaceAvatar(
                name = name,
                size = 28.dp,
                modifier = Modifier.clearAndSetSemantics { testTag = "workspace-avatar" },
            )
        }
        Text(
            text = label,
            style = MaterialTheme.typography.titleLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
        if (affordance == WorkspaceAffordance.CHEVRON) {
            Icon(
                imageVector = Icons.Default.KeyboardArrowDown,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.clearAndSetSemantics { testTag = "workspace-chevron" },
            )
        }
    }
}

/** Circular first-letter badge; the letter is decorative, so callers strip its semantics. */
@Composable
private fun WorkspaceAvatar(
    name: String?,
    size: Dp,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .size(size)
            .background(MaterialTheme.colorScheme.primaryContainer, CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = name?.trim()?.firstOrNull()?.uppercase() ?: "?",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onPrimaryContainer,
        )
    }
}

/**
 * Workspace picker in the container chosen by [LocalDesignVariants]. Must be composed inside the
 * box that hosts [WorkspaceTitle] so the dropdown variant anchors to it; the sheet and full-screen
 * variants render in their own window regardless of placement.
 */
@Composable
fun WorkspaceSwitcher(
    state: TasksUiState,
    onQueryChanged: (String) -> Unit,
    onSelect: (String) -> Unit,
    onRetry: () -> Unit,
    onScanDifferentCode: () -> Unit,
    onDismiss: () -> Unit,
) {
    val content: @Composable ColumnScope.(listLayout: WorkspaceListLayout) -> Unit = { listLayout ->
        WorkspaceSwitcherContent(
            state = state,
            onQueryChanged = onQueryChanged,
            onSelect = onSelect,
            onRetry = onRetry,
            onScanDifferentCode = onScanDifferentCode,
            listLayout = listLayout,
        )
    }
    when (LocalDesignVariants.current.switcherContainer) {
        SwitcherContainer.BOTTOM_SHEET -> ModalBottomSheet(
            onDismissRequest = onDismiss,
            sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
            modifier = Modifier.testTag("workspace-switcher"),
        ) {
            Text(
                text = "Workspaces",
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = SwitcherHorizontalPadding, vertical = 4.dp),
            )
            content(WorkspaceListLayout.Lazy(Modifier.weight(1f, fill = false)))
        }

        // DropdownMenu sizes itself from intrinsics, which lazy lists cannot provide, so the
        // menu's own scrolling column holds the rows instead.
        SwitcherContainer.DROPDOWN -> DropdownMenu(
            expanded = true,
            onDismissRequest = onDismiss,
            modifier = Modifier
                .width(300.dp)
                .heightIn(max = 480.dp)
                .testTag("workspace-switcher"),
        ) {
            content(WorkspaceListLayout.Eager)
        }

        SwitcherContainer.FULL_SCREEN -> Dialog(
            onDismissRequest = onDismiss,
            properties = DialogProperties(usePlatformDefaultWidth = false),
        ) {
            Surface(modifier = Modifier.fillMaxSize()) {
                FullScreenSwitcher(onDismiss = onDismiss, content = content)
            }
        }
    }
}

/** How the switcher lays out its workspace rows; depends on what the container can measure. */
private sealed interface WorkspaceListLayout {
    data class Lazy(val modifier: Modifier) : WorkspaceListLayout

    data object Eager : WorkspaceListLayout
}

@Composable
private fun FullScreenSwitcher(
    onDismiss: () -> Unit,
    content: @Composable ColumnScope.(listLayout: WorkspaceListLayout) -> Unit,
) {
    Column(modifier = Modifier.testTag("workspace-switcher")) {
        TopAppBar(
            title = { Text("Workspaces") },
            navigationIcon = {
                IconButton(onClick = onDismiss, modifier = Modifier.testTag("workspace-switcher-close")) {
                    Icon(Icons.Default.Close, contentDescription = "Close workspace picker")
                }
            },
            colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface),
        )
        content(WorkspaceListLayout.Lazy(Modifier.weight(1f)))
    }
}

@Composable
private fun ColumnScope.WorkspaceSwitcherContent(
    state: TasksUiState,
    onQueryChanged: (String) -> Unit,
    onSelect: (String) -> Unit,
    onRetry: () -> Unit,
    onScanDifferentCode: () -> Unit,
    listLayout: WorkspaceListLayout,
) {
    WorkspaceFilterField(query = state.switcherQuery, onQueryChanged = onQueryChanged)
    HorizontalDivider(thickness = Dp.Hairline, color = MaterialTheme.colorScheme.outlineVariant)
    val error = state.workspacesError
    when {
        error != null -> SwitcherMessage {
            Text(
                text = error,
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
                modifier = Modifier.testTag("workspaces-error"),
            )
            Spacer(modifier = Modifier.height(12.dp))
            Button(onClick = onRetry, modifier = Modifier.testTag("retry-workspaces")) {
                Text("Retry")
            }
        }

        state.isLoadingWorkspaces && state.workspaces.isEmpty() -> SwitcherMessage {
            CircularProgressIndicator(modifier = Modifier.testTag("workspaces-loading"))
        }

        state.workspaces.isEmpty() -> SwitcherMessage {
            MutedText(text = "No workspaces found.", tag = "empty-workspaces")
        }

        state.filteredWorkspaces.isEmpty() -> SwitcherMessage {
            MutedText(text = "No workspaces match “${state.switcherQuery.trim()}”.", tag = "workspace-filter-empty")
        }

        else -> WorkspaceList(
            workspaces = state.filteredWorkspaces,
            currentId = state.currentWorkspace?.id,
            onSelect = onSelect,
            layout = listLayout,
        )
    }
    HorizontalDivider(thickness = Dp.Hairline, color = MaterialTheme.colorScheme.outlineVariant)
    Text(
        text = "Scan a different pairing code",
        style = MaterialTheme.typography.bodyLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onScanDifferentCode, role = Role.Button)
            .testTag("scan-different-code")
            .padding(horizontal = SwitcherHorizontalPadding, vertical = 14.dp),
    )
}

@Composable
private fun WorkspaceList(
    workspaces: List<Workspace>,
    currentId: String?,
    onSelect: (String) -> Unit,
    layout: WorkspaceListLayout,
) {
    val row: @Composable (Workspace) -> Unit = { workspace ->
        WorkspaceRow(
            workspace = workspace,
            selected = workspace.id == currentId,
            onClick = { onSelect(workspace.id) },
        )
    }
    when (layout) {
        is WorkspaceListLayout.Lazy -> LazyColumn(modifier = layout.modifier.fillMaxWidth()) {
            items(workspaces, key = { it.id }) { row(it) }
        }

        WorkspaceListLayout.Eager -> Column(modifier = Modifier.fillMaxWidth()) {
            workspaces.forEach { row(it) }
        }
    }
}

@Composable
private fun WorkspaceFilterField(
    query: String,
    onQueryChanged: (String) -> Unit,
) {
    TextField(
        value = query,
        onValueChange = onQueryChanged,
        modifier = Modifier
            .fillMaxWidth()
            .testTag("workspace-filter"),
        placeholder = { Text("Filter workspaces") },
        singleLine = true,
        leadingIcon = { Icon(Icons.Default.Search, contentDescription = null) },
        trailingIcon = {
            if (query.isNotEmpty()) {
                IconButton(
                    onClick = { onQueryChanged("") },
                    modifier = Modifier.testTag("workspace-filter-clear"),
                ) {
                    Icon(Icons.Default.Close, contentDescription = "Clear filter")
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
}

@Composable
private fun WorkspaceRow(
    workspace: Workspace,
    selected: Boolean,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .selectable(selected = selected, onClick = onClick)
            .testTag("workspace-" + workspace.id)
            .padding(horizontal = SwitcherHorizontalPadding, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        WorkspaceAvatar(
            name = workspace.name,
            size = 32.dp,
            modifier = Modifier.clearAndSetSemantics { },
        )
        Text(
            text = workspace.name,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = if (selected) FontWeight.SemiBold else null,
            color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (selected) {
            Icon(
                imageVector = Icons.Default.Check,
                contentDescription = "Current workspace",
                tint = MaterialTheme.colorScheme.primary,
            )
        }
    }
}

@Composable
private fun SwitcherMessage(content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = SwitcherHorizontalPadding, vertical = 32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        content = content,
    )
}

@Composable
private fun MutedText(text: String, tag: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
        modifier = Modifier.testTag(tag),
    )
}

/** Every affordance × container pairing, for previews and screenshot comparisons. */
internal val switcherVariants: List<DesignVariants> =
    WorkspaceAffordance.entries.flatMap { affordance ->
        SwitcherContainer.entries.map { container ->
            DesignVariants(workspaceAffordance = affordance, switcherContainer = container)
        }
    }

internal fun sampleWorkspaces(): List<Workspace> = listOf(
    Workspace(id = "ws-sidekick", name = "Sidekick"),
    Workspace(id = "ws-android", name = "Android companion"),
    Workspace(id = "ws-docs", name = "Docs site"),
    Workspace(id = "ws-infra", name = "Infra playground"),
)

private class SwitcherVariantPreviewProvider : PreviewParameterProvider<DesignVariants> {
    override val values: Sequence<DesignVariants> = switcherVariants.asSequence()
}

@Composable
private fun WorkspaceSwitcherPreview(variants: DesignVariants, darkTheme: Boolean) {
    val workspaces = sampleWorkspaces()
    val state = TasksUiState(
        workspaces = workspaces,
        isLoadingWorkspaces = false,
        currentWorkspace = workspaces.first(),
    )
    DesignVariantsProvider(initial = variants) {
        AppTheme(darkTheme = darkTheme) {
            Scaffold(
                topBar = {
                    TopAppBar(
                        title = {
                            Box {
                                WorkspaceTitle(state = state, onClick = {})
                                WorkspaceSwitcher(
                                    state = state,
                                    onQueryChanged = {},
                                    onSelect = {},
                                    onRetry = {},
                                    onScanDifferentCode = {},
                                    onDismiss = {},
                                )
                            }
                        },
                        colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface),
                    )
                },
            ) { padding ->
                Box(modifier = Modifier.padding(padding))
            }
        }
    }
}

@Preview(name = "Switcher light", showBackground = true, widthDp = 360, heightDp = 640)
@Composable
private fun WorkspaceSwitcherLightPreview(
    @PreviewParameter(SwitcherVariantPreviewProvider::class) variants: DesignVariants,
) {
    WorkspaceSwitcherPreview(variants = variants, darkTheme = false)
}

@Preview(name = "Switcher dark", showBackground = true, widthDp = 360, heightDp = 640)
@Composable
private fun WorkspaceSwitcherDarkPreview(
    @PreviewParameter(SwitcherVariantPreviewProvider::class) variants: DesignVariants,
) {
    WorkspaceSwitcherPreview(variants = variants, darkTheme = true)
}