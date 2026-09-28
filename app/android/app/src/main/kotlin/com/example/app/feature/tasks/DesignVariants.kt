package com.example.app.feature.tasks

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import com.example.app.BuildConfig

enum class WorkspaceAffordance { CHEVRON, AVATAR }

enum class SwitcherContainer { BOTTOM_SHEET, DROPDOWN, FULL_SCREEN }

enum class BucketPresentation {
    /** Needs attention / Active sections with small headers. */
    HEADERS,

    /** One continuous list; ordering and status indicators carry the grouping. */
    NO_HEADERS,

    /** Single-select Open / Drafts / Done chips showing one group at a time. */
    TOP_CHIPS,
}

enum class CollapsedAccess {
    /** Collapsed Drafts / Done rows at the end of the list. */
    BOTTOM,

    /** Drafts / Done toggle chips above the list. */
    TOP,
}

/**
 * Open UX choices for the Tasks screen. Defaults are the wireframe-approved picks; the other
 * values exist so the Compose renderings can be compared side by side before settling.
 * With [BucketPresentation.TOP_CHIPS] the chips are the single-group filter, and
 * [CollapsedAccess] decides whether that filter bar sits above or below the list.
 */
data class DesignVariants(
    val workspaceAffordance: WorkspaceAffordance = WorkspaceAffordance.CHEVRON,
    val switcherContainer: SwitcherContainer = SwitcherContainer.FULL_SCREEN,
    val bucketPresentation: BucketPresentation = BucketPresentation.TOP_CHIPS,
    val collapsedAccess: CollapsedAccess = CollapsedAccess.TOP,
)

val LocalDesignVariants = compositionLocalOf { DesignVariants() }

/** Non-null only in debug builds, where the overflow menu can switch variants at runtime. */
internal val LocalDesignVariantsEditor = staticCompositionLocalOf<((DesignVariants) -> Unit)?> { null }

/** Every distinct list layout, for previews and screenshot comparisons. */
internal val bucketLayoutVariants: List<DesignVariants> = listOf(
    DesignVariants(bucketPresentation = BucketPresentation.HEADERS, collapsedAccess = CollapsedAccess.BOTTOM),
    DesignVariants(bucketPresentation = BucketPresentation.HEADERS, collapsedAccess = CollapsedAccess.TOP),
    DesignVariants(bucketPresentation = BucketPresentation.NO_HEADERS, collapsedAccess = CollapsedAccess.BOTTOM),
    DesignVariants(bucketPresentation = BucketPresentation.NO_HEADERS, collapsedAccess = CollapsedAccess.TOP),
    DesignVariants(bucketPresentation = BucketPresentation.TOP_CHIPS, collapsedAccess = CollapsedAccess.BOTTOM),
    DesignVariants(bucketPresentation = BucketPresentation.TOP_CHIPS, collapsedAccess = CollapsedAccess.TOP),
)

@Composable
fun DesignVariantsProvider(
    initial: DesignVariants = DesignVariants(),
    content: @Composable () -> Unit,
) {
    var variants by remember { mutableStateOf(initial) }
    val editor: ((DesignVariants) -> Unit)? =
        if (BuildConfig.DEBUG) { updated -> variants = updated } else null
    CompositionLocalProvider(
        LocalDesignVariants provides variants,
        LocalDesignVariantsEditor provides editor,
        content = content,
    )
}

@Composable
internal fun DesignVariantSwitcherDialog(
    current: DesignVariants,
    onChange: (DesignVariants) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = {
            TextButton(onClick = onDismiss, modifier = Modifier.testTag("design-variants-done")) {
                Text("Done")
            }
        },
        title = { Text("Design variants") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                VariantChoice(
                    label = "Workspace affordance",
                    options = WorkspaceAffordance.entries,
                    selected = current.workspaceAffordance,
                    onSelect = { onChange(current.copy(workspaceAffordance = it)) },
                )
                VariantChoice(
                    label = "Switcher container",
                    options = SwitcherContainer.entries,
                    selected = current.switcherContainer,
                    onSelect = { onChange(current.copy(switcherContainer = it)) },
                )
                VariantChoice(
                    label = "Bucket presentation",
                    options = BucketPresentation.entries,
                    selected = current.bucketPresentation,
                    onSelect = { onChange(current.copy(bucketPresentation = it)) },
                )
                VariantChoice(
                    label = "Drafts / Done access",
                    options = CollapsedAccess.entries,
                    selected = current.collapsedAccess,
                    onSelect = { onChange(current.copy(collapsedAccess = it)) },
                )
            }
        },
    )
}

@Composable
private fun <T : Enum<T>> VariantChoice(
    label: String,
    options: List<T>,
    selected: T,
    onSelect: (T) -> Unit,
) {
    Text(
        text = label,
        style = MaterialTheme.typography.titleSmall,
        modifier = Modifier.padding(top = 12.dp, bottom = 4.dp),
    )
    options.forEach { option ->
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .selectable(
                    selected = option == selected,
                    role = Role.RadioButton,
                    onClick = { onSelect(option) },
                )
                .padding(vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RadioButton(selected = option == selected, onClick = null)
            Text(
                text = option.displayName(),
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.padding(start = 8.dp),
            )
        }
    }
}

private fun Enum<*>.displayName(): String =
    name.lowercase().replace('_', ' ').replaceFirstChar { it.uppercase() }