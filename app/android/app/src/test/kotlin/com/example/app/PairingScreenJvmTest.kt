package com.example.app

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import com.example.app.core.remote.Workspace
import com.example.app.feature.pairing.PairingNavigationEffect
import com.example.app.feature.pairing.PairingScreen
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.example.app.feature.pairing.PairingUiState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class PairingScreenJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun workspaceBeyondViewportCanBeScrolledToAndSelected() {
        val workspaces = List(40) { Workspace(id = "$it", name = "Workspace $it") }
        var selectedWorkspace: Workspace? = null
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(
                    isPaired = true,
                    isLoading = false,
                    workspaces = workspaces,
                ),
                onScan = {},
                onRetry = {},
                onWorkspaceSelected = { selectedWorkspace = it },
            )
        }

        composeRule.onNodeWithTag("workspace-39")
            .performScrollTo()
            .assertIsDisplayed()
            .performClick()

        assertEquals(workspaces.last(), selectedWorkspace)
    }

    @Test
    fun hintedWorkspaceOpensOnceAndBackAllowsManualSelection() {
        val workspace = Workspace(id = "one", name = "One")
        var opens = 0
        composeRule.setContent {
            var pending by remember { mutableStateOf<Workspace?>(workspace) }
            var showingTasks by remember { mutableStateOf(false) }
            if (showingTasks) {
                Button(
                    onClick = { showingTasks = false },
                    modifier = Modifier.testTag("back"),
                ) { Text("Back") }
            } else {
                PairingNavigationEffect(
                    workspace = pending,
                    onConsumed = { pending = null },
                    onWorkspaceSelected = { opens++; showingTasks = true },
                )
                PairingScreen(
                    state = PairingUiState(
                        isPaired = true,
                        isLoading = false,
                        workspaces = listOf(workspace),
                    ),
                    onScan = {},
                    onRetry = {},
                    onWorkspaceSelected = { opens++; showingTasks = true },
                )
            }
        }
        composeRule.onNodeWithTag("back").assertIsDisplayed().performClick()
        composeRule.onNodeWithTag("workspace-one").assertIsDisplayed()
        composeRule.runOnIdle { assertEquals(1, opens) }
        composeRule.onNodeWithTag("workspace-one").performClick()
        composeRule.onNodeWithTag("back").assertIsDisplayed()
        composeRule.runOnIdle { assertEquals(2, opens) }
    }

    @Test
    fun unpairedStateStartsScanner() {
        var scanned = false
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(isLoading = false),
                onScan = { scanned = true },
                onRetry = {},
                onWorkspaceSelected = {},
            )
        }

        composeRule.onNodeWithTag("scan-pairing-code").performClick()

        assertTrue(scanned)
    }

    @Test
    fun loadingStateShowsProgress() {
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(isPaired = true, isLoading = true),
                onScan = {},
                onRetry = {},
                onWorkspaceSelected = {},
            )
        }

        composeRule.onNodeWithTag("pairing-loading").assertIsDisplayed()
    }

    @Test
    fun errorStateOffersRetry() {
        var retries = 0
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(
                    isPaired = true,
                    isLoading = false,
                    errorMessage = "Unable to load",
                ),
                onScan = {},
                onRetry = { retries += 1 },
                onWorkspaceSelected = {},
            )
        }

        composeRule.onNodeWithTag("pairing-error").assertTextEquals("Unable to load")
        composeRule.onNodeWithTag("retry-workspaces").performClick()

        assertEquals(1, retries)
    }

    @Test
    fun emptyWorkspaceStateShowsMessage() {
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(
                    isPaired = true,
                    isLoading = false,
                ),
                onScan = {},
                onRetry = {},
                onWorkspaceSelected = {},
            )
        }

        composeRule.onNodeWithTag("empty-workspaces")
            .assertTextEquals("No workspaces are available.")
    }

    @Test
    fun unpairedErrorStateKeepsPairingActionAvailable() {
        var scans = 0
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(
                    isLoading = false,
                    errorMessage = "Unable to load pairing",
                ),
                onScan = { scans += 1 },
                onRetry = {},
                onWorkspaceSelected = {},
            )
        }

        composeRule.onNodeWithTag("pairing-error")
            .assertTextEquals("Unable to load pairing")
        composeRule.onNodeWithTag("scan-pairing-code").performClick()

        assertEquals(1, scans)
    }

    @Test
    fun pairedErrorStateOffersAlternateScan() {
        var scans = 0
        composeRule.setContent {
            PairingScreen(
                state = PairingUiState(
                    isPaired = true,
                    isLoading = false,
                    errorMessage = "Unable to load workspaces",
                ),
                onScan = { scans += 1 },
                onRetry = {},
                onWorkspaceSelected = {},
            )
        }

        composeRule.onNodeWithTag("scan-different-code").performClick()

        assertEquals(1, scans)
    }

    @Test
    fun workspaceClickUpdatesHoistedSelection() {
        val workspace = Workspace(id = "workspace-1", name = "First workspace")
        var selectedWorkspace: Workspace? = null
        composeRule.setContent {
            var state by remember {
                mutableStateOf(
                    PairingUiState(
                        isPaired = true,
                        isLoading = false,
                        workspaces = listOf(workspace),
                    ),
                )
            }
            PairingScreen(
                state = state,
                onScan = {},
                onRetry = {},
                onWorkspaceSelected = {
                    selectedWorkspace = it
                    state = state.copy(selectedWorkspaceId = it.id)
                },
            )
        }

        composeRule.onNodeWithTag("workspace-workspace-1").performClick()
        composeRule.onNodeWithTag("workspace-workspace-1").assertIsSelected()

        assertEquals(workspace, selectedWorkspace)
    }
}