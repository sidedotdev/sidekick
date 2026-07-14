package com.example.app

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * On-device entry-point coverage for the pairing flow. Requires a running
 * device/emulator (KVM), so it is intentionally not executed in the headless
 * container; run it via `connectedDebugAndroidTest` on a device or a device farm.
 */
@RunWith(AndroidJUnit4::class)
class PairingEntryInstrumentedTest {

    @get:Rule
    val composeRule = createAndroidComposeRule<MainActivity>()

    @Test
    fun pairingEntryScreenIsDisplayed() {
        composeRule.onNodeWithText("Connect to Sidekick").assertIsDisplayed()
        composeRule.onNodeWithTag("scan-pairing-code")
            .assertIsDisplayed()
            .assertTextEquals("Scan pairing code")
    }
}