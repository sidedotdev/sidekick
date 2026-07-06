package com.example.app

import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * On-device end-to-end counterpart to [CounterScreenJvmTest]. Requires a running
 * device/emulator (KVM), so it is intentionally not executed in the headless
 * container; run it via `connectedDebugAndroidTest` on a device or a device farm.
 */
@RunWith(AndroidJUnit4::class)
class CounterScreenInstrumentedTest {

    @get:Rule
    val composeRule = createAndroidComposeRule<MainActivity>()

    @Test
    fun clickingIncrementUpdatesCount() {
        composeRule.onNodeWithTag("counter").assertTextEquals("0")
        composeRule.onNodeWithTag("increment").performClick()
        composeRule.onNodeWithTag("counter").assertTextEquals("1")
    }
}