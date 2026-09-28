package com.example.app.testing

import android.graphics.Bitmap
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onRoot
import java.io.File

private val screenshotDir: File =
    File(System.getProperty("screenshots.dir") ?: "build/screenshots")

/**
 * Writes the current composition to `build/screenshots/<name>.png` (relative to the Gradle
 * module) for visual review. The test class must run under
 * `@GraphicsMode(GraphicsMode.Mode.NATIVE)` so Robolectric renders real pixels, and
 * Compose ui-test >= 1.12 is required for captureToImage to work on Robolectric at all.
 */
fun ComposeContentTestRule.captureScreenshot(name: String): File {
    waitForIdle()
    val bitmap = onRoot().captureToImage().asAndroidBitmap()
    val file = File(screenshotDir, "$name.png")
    file.parentFile?.mkdirs()
    file.outputStream().use { out ->
        bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
    }
    return file
}