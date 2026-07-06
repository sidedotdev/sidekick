package com.example.app

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.example.app.feature.counter.CounterScreen
import com.example.app.feature.counter.CounterUiState
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CounterScreenJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun clickingIncrementUpdatesCount() {
        composeRule.setContent {
            var state by remember { mutableStateOf(CounterUiState(count = 0)) }
            CounterScreen(
                state = state,
                onIncrement = { state = state.copy(count = state.count + 1) },
            )
        }

        composeRule.onNodeWithTag("counter").assertTextEquals("0")
        composeRule.onNodeWithTag("increment").performClick()
        composeRule.onNodeWithTag("counter").assertTextEquals("1")
    }
}