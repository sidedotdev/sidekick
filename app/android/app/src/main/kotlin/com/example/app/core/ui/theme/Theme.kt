package com.example.app.core.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.tooling.preview.Preview

enum class StatusChipStyle {
    /** Coloured dot followed by a muted label. */
    DOT,

    /** Label on a translucent tint of the status colour. */
    TINTED,
}

enum class RowDividerStyle {
    /** Hairline spanning the full row width. */
    FULL_WIDTH,

    /** Hairline inset from the leading edge, so rows read as one continuous list. */
    INSET,
}

/**
 * Minimal neutral-plus-accent styles. Each variant pairs a neutral palette and accent
 * with the row and status-chip treatments that suit it.
 */
enum class StyleVariant(
    internal val lightNeutral: NeutralPalette,
    internal val darkNeutral: NeutralPalette,
    internal val lightAccent: AccentPalette,
    internal val darkAccent: AccentPalette,
    val statusChipStyle: StatusChipStyle,
    val rowDividerStyle: RowDividerStyle,
) {
    GRAPHITE(
        lightNeutral = NeutralLight,
        darkNeutral = NeutralDark,
        lightAccent = GraphiteAccentLight,
        darkAccent = GraphiteAccentDark,
        statusChipStyle = StatusChipStyle.DOT,
        rowDividerStyle = RowDividerStyle.FULL_WIDTH,
    ),
    MOSS(
        lightNeutral = MossNeutralLight,
        darkNeutral = MossNeutralDark,
        lightAccent = MossAccentLight,
        darkAccent = MossAccentDark,
        statusChipStyle = StatusChipStyle.TINTED,
        rowDividerStyle = RowDividerStyle.INSET,
    ),
}

val LocalStyleVariant = staticCompositionLocalOf { StyleVariant.GRAPHITE }

internal fun appColorScheme(styleVariant: StyleVariant, darkTheme: Boolean): ColorScheme {
    val neutral = if (darkTheme) styleVariant.darkNeutral else styleVariant.lightNeutral
    val accent = if (darkTheme) styleVariant.darkAccent else styleVariant.lightAccent
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
    styleVariant: StyleVariant = StyleVariant.GRAPHITE,
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    CompositionLocalProvider(LocalStyleVariant provides styleVariant) {
        MaterialTheme(
            colorScheme = appColorScheme(styleVariant, darkTheme),
            typography = AppTypography,
            content = content,
        )
    }
}

@Preview(name = "Graphite light", showBackground = true)
@Composable
private fun GraphiteLightPreview() {
    AppTheme(styleVariant = StyleVariant.GRAPHITE, darkTheme = false) { ThemeSample() }
}

@Preview(name = "Graphite dark", showBackground = true)
@Composable
private fun GraphiteDarkPreview() {
    AppTheme(styleVariant = StyleVariant.GRAPHITE, darkTheme = true) { ThemeSample() }
}

@Preview(name = "Moss light", showBackground = true)
@Composable
private fun MossLightPreview() {
    AppTheme(styleVariant = StyleVariant.MOSS, darkTheme = false) { ThemeSample() }
}

@Preview(name = "Moss dark", showBackground = true)
@Composable
private fun MossDarkPreview() {
    AppTheme(styleVariant = StyleVariant.MOSS, darkTheme = true) { ThemeSample() }
}