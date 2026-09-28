package com.example.app.testing

import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.ViewRootForTest
import androidx.compose.ui.test.SemanticsNodeInteraction
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
    return onRoot().writeScreenshot(name)
}

fun SemanticsNodeInteraction.writeScreenshot(name: String): File =
    writePng(name, captureToImage().asAndroidBitmap())

/**
 * Draws the whole window that hosts this node. Dialogs, bottom sheets and menus live in their
 * own window; under Robolectric, `captureToImage` of a node offset within such a window (e.g. a
 * bottom sheet anchored at the bottom) returned a bitmap of the node's size read from the window
 * origin, so the decor view is rendered directly instead.
 */
fun SemanticsNodeInteraction.writeWindowScreenshot(name: String): File {
    val decor = (fetchSemanticsNode().root as ViewRootForTest).view.rootView
    val bitmap = Bitmap.createBitmap(decor.width, decor.height, Bitmap.Config.ARGB_8888)
    decor.draw(Canvas(bitmap))
    return writePng(name, bitmap)
}

private fun writePng(name: String, bitmap: Bitmap): File {
    val file = File(screenshotDir, "$name.png")
    file.parentFile?.mkdirs()
    file.outputStream().use { out ->
        bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
    }
    return file
}