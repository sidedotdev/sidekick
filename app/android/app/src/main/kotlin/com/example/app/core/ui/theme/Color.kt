package com.example.app.core.ui.theme

import androidx.compose.ui.graphics.Color

internal data class NeutralPalette(
    val background: Color,
    val surfaceContainerLowest: Color,
    val surfaceContainerLow: Color,
    val surfaceContainer: Color,
    val surfaceContainerHigh: Color,
    val surfaceContainerHighest: Color,
    val onSurface: Color,
    val onSurfaceVariant: Color,
    val outline: Color,
    val outlineVariant: Color,
)

internal data class AccentPalette(
    val accent: Color,
    val onAccent: Color,
)

internal val NeutralLight = NeutralPalette(
    background = Color(0xFFFAFAFA),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF5F5F7),
    surfaceContainer = Color(0xFFF0F0F2),
    surfaceContainerHigh = Color(0xFFE8E8EC),
    surfaceContainerHighest = Color(0xFFE0E0E5),
    onSurface = Color(0xFF1B1B1F),
    onSurfaceVariant = Color(0xFF6E6E76),
    outline = Color(0xFFC4C4CC),
    outlineVariant = Color(0xFFE6E6EA),
)

internal val NeutralDark = NeutralPalette(
    background = Color(0xFF101013),
    surfaceContainerLowest = Color(0xFF0B0B0D),
    surfaceContainerLow = Color(0xFF1A1A1E),
    surfaceContainer = Color(0xFF25252A),
    surfaceContainerHigh = Color(0xFF2E2E34),
    surfaceContainerHighest = Color(0xFF38383F),
    onSurface = Color(0xFFECECF1),
    onSurfaceVariant = Color(0xFF9A9AA4),
    outline = Color(0xFF4A4A52),
    outlineVariant = Color(0xFF2B2B31),
)

// Matches the web frontend CTA/brand purple (--color-primary: rgb(131, 58, 180)),
// lifted in dark mode so text on it stays legible.
internal val AccentLight = AccentPalette(Color(0xFF833AB4), Color(0xFFFFFFFF))
internal val AccentDark = AccentPalette(Color(0xFFC58BF0), Color(0xFF2E0A4A))

internal val ErrorLight = Color(0xFFC92A2A)
internal val OnErrorLight = Color(0xFFFFFFFF)
internal val ErrorDark = Color(0xFFFF8A8A)
internal val OnErrorDark = Color(0xFF3B0000)

// Status-only colours; kept out of the colour scheme so the accent stays the single brand hue.
internal val AmberLight = Color(0xFFB7791F)
internal val AmberDark = Color(0xFFF2C35B)
internal val GreenLight = Color(0xFF2B8A3E)
internal val GreenDark = Color(0xFF8CE99A)