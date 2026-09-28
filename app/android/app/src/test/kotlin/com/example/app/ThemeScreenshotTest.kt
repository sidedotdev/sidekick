package com.example.app

import androidx.compose.ui.test.junit4.createComposeRule
import com.example.app.core.ui.theme.AppTheme
import com.example.app.core.ui.theme.ThemeSample
import com.example.app.testing.captureScreenshot
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.ParameterizedRobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(ParameterizedRobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ThemeScreenshotTest(private val darkTheme: Boolean) {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun rendersThemeSample() {
        composeRule.setContent {
            AppTheme(darkTheme = darkTheme) {
                ThemeSample()
            }
        }

        val mode = if (darkTheme) "dark" else "light"
        val file = composeRule.captureScreenshot("theme-$mode")

        assertTrue("screenshot written to ${file.path}", file.length() > 0)
    }

    companion object {
        @JvmStatic
        @ParameterizedRobolectricTestRunner.Parameters(name = "dark={0}")
        fun parameters(): List<Array<Any>> = listOf(arrayOf(false), arrayOf(true))
    }
}