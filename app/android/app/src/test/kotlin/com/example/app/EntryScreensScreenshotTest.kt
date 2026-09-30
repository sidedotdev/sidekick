package com.example.app

import androidx.compose.ui.test.junit4.createComposeRule
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.pairing.PairingScreen
import com.example.app.feature.pairing.PairingUiState
import com.example.app.feature.taskdetail.TaskDetailScreen
import com.example.app.feature.taskdetail.sampleDetailTask
import com.example.app.testing.captureScreenshot
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.ParameterizedRobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/** Renders the Pairing and Task Detail screens in light and dark for visual review. */
@RunWith(ParameterizedRobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class EntryScreensScreenshotTest(private val darkTheme: Boolean) {

    @get:Rule
    val composeRule = createComposeRule()

    private val mode = if (darkTheme) "dark" else "light"

    @Test
    fun rendersPairing() {
        composeRule.setContent {
            AppTheme(darkTheme = darkTheme) {
                PairingScreen(state = PairingUiState(isCheckingStoredPairing = false), onScan = {})
            }
        }

        val file = composeRule.captureScreenshot("pairing-$mode")
        assertTrue("screenshot written to ${file.path}", file.length() > 0)
    }

    @Test
    fun rendersTaskDetail() {
        composeRule.setContent {
            AppTheme(darkTheme = darkTheme) {
                TaskDetailScreen(task = sampleDetailTask(), isLoading = false, onBack = {})
            }
        }

        val file = composeRule.captureScreenshot("task-detail-$mode")
        assertTrue("screenshot written to ${file.path}", file.length() > 0)
    }

    companion object {
        @JvmStatic
        @ParameterizedRobolectricTestRunner.Parameters(name = "dark={0}")
        fun parameters(): List<Array<Any>> = listOf(arrayOf(false), arrayOf(true))
    }
}