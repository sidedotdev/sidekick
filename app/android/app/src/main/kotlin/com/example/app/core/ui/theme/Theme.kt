package com.example.app.core.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.tooling.preview.Preview

/** Neutral greys with the Sidekick brand purple as the single accent. */
internal fun appColorScheme(darkTheme: Boolean): ColorScheme {
    val neutral = if (darkTheme) NeutralDark else NeutralLight
    val accent = if (darkTheme) AccentDark else AccentLight
    val error = if (darkTheme) ErrorDark else ErrorLight
    val onError = if (darkTheme) OnErrorDark else OnErrorLight
    val base = if (darkTheme) darkColorScheme() else lightColorScheme()
    return base.copy(
        primary = accent.accent,
        onPrimary = accent.onAccent,
        primaryContainer = accent.accent.copy(alpha = 0.16f).compositeOver(neutral.background),
        onPrimaryContainer = accent.accent,
        inversePrimary = accent.accent,
        secondary = neutral.onSurfaceVariant,
        onSecondary = neutral.background,
        secondaryContainer = neutral.surfaceContainer,
        onSecondaryContainer = neutral.onSurface,
        tertiary = accent.accent,
        onTertiary = accent.onAccent,
        tertiaryContainer = neutral.surfaceContainer,
        onTertiaryContainer = neutral.onSurface,
        background = neutral.background,
        onBackground = neutral.onSurface,
        surface = neutral.background,
        onSurface = neutral.onSurface,
        surfaceVariant = neutral.surfaceContainer,
        onSurfaceVariant = neutral.onSurfaceVariant,
        surfaceTint = accent.accent,
        surfaceContainerLowest = neutral.surfaceContainerLowest,
        surfaceContainerLow = neutral.surfaceContainerLow,
        surfaceContainer = neutral.surfaceContainer,
        surfaceContainerHigh = neutral.surfaceContainerHigh,
        surfaceContainerHighest = neutral.surfaceContainerHighest,
        surfaceDim = neutral.surfaceContainerLow,
        surfaceBright = neutral.surfaceContainerLowest,
        inverseSurface = neutral.onSurface,
        inverseOnSurface = neutral.background,
        outline = neutral.outline,
        outlineVariant = neutral.outlineVariant,
        error = error,
        onError = onError,
        errorContainer = error.copy(alpha = 0.16f).compositeOver(neutral.background),
        onErrorContainer = error,
    )
}

@Composable
fun AppTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = appColorScheme(darkTheme),
        typography = AppTypography,
        content = content,
    )
}

@Preview(name = "Theme light", showBackground = true)
@Composable
private fun ThemeLightPreview() {
    AppTheme(darkTheme = false) { ThemeSample() }
}

@Preview(name = "Theme dark", showBackground = true)
@Composable
private fun ThemeDarkPreview() {
    AppTheme(darkTheme = true) { ThemeSample() }
}