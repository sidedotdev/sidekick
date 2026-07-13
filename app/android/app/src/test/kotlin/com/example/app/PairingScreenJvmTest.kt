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
import com.example.app.core.remote.Workspace
import com.example.app.feature.pairing.PairingScreen
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