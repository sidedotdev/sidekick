package com.example.app

import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.pairing.PairingNavigationEffect
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
            AppTheme(darkTheme = false) {
                PairingScreen(
                    state = PairingUiState(isCheckingStoredPairing = false),
                    onScan = { scanned = true },
                )
            }
        }

        composeRule.onNodeWithText("Connect to Sidekick").assertIsDisplayed()
        composeRule.onNodeWithTag("scan-pairing-code")
            .assertTextEquals("Scan pairing code")
            .performClick()

        assertTrue(scanned)
    }

    @Test
    fun checkingStoredPairingShowsProgressWithoutTheScanner() {
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                PairingScreen(
                    state = PairingUiState(isCheckingStoredPairing = true),
                    onScan = {},
                )
            }
        }

        composeRule.onNodeWithTag("pairing-loading").assertIsDisplayed()
        composeRule.onNodeWithTag("scan-pairing-code").assertDoesNotExist()
    }

    @Test
    fun unpairedErrorStateKeepsPairingActionAvailable() {
        var scans = 0
        composeRule.setContent {
            AppTheme(darkTheme = false) {
                PairingScreen(
                    state = PairingUiState(
                        isCheckingStoredPairing = false,
                        errorMessage = "Unable to load pairing",
                    ),
                    onScan = { scans += 1 },
                )
            }
        }

        composeRule.onNodeWithTag("pairing-error")
            .assertTextEquals("Unable to load pairing")
        composeRule.onNodeWithTag("scan-pairing-code").performClick()

        assertEquals(1, scans)
    }

    @Test
    fun pairedStateShowsProgressAndReportsThePairingOnceWithItsHint() {
        val hints = mutableListOf<String?>()
        composeRule.setContent {
            var state by remember { mutableStateOf(PairingUiState(isCheckingStoredPairing = true)) }
            AppTheme(darkTheme = false) {
                PairingNavigationEffect(state = state, onPaired = { hints += it })
                PairingScreen(state = state, onScan = {})
                Button(
                    onClick = {
                        state = PairingUiState(
                            isCheckingStoredPairing = false,
                            isPaired = true,
                            hintedWorkspaceId = "ws-2",
                        )
                    },
                    modifier = Modifier.testTag("pair"),
                ) { Text("Pair") }
            }
        }

        composeRule.runOnIdle { assertTrue(hints.isEmpty()) }
        composeRule.onNodeWithTag("pair").performClick()

        composeRule.onNodeWithTag("pairing-loading").assertIsDisplayed()
        composeRule.onNodeWithTag("scan-pairing-code").assertDoesNotExist()
        composeRule.runOnIdle { assertEquals(listOf<String?>("ws-2"), hints) }
    }
}