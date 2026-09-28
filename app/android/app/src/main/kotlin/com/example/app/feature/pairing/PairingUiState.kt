package com.example.app.feature.pairing

data class PairingUiState(
    val isCheckingStoredPairing: Boolean = true,
    val isPaired: Boolean = false,
    /** Workspace the scanned QR code pointed at, forwarded to the Tasks screen. */
    val hintedWorkspaceId: String? = null,
    val errorMessage: String? = null,
)