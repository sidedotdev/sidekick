package com.example.app.feature.pairing

import com.example.app.core.remote.Workspace

data class PairingUiState(
    val isPaired: Boolean = false,
    val isLoading: Boolean = true,
    val workspaces: List<Workspace> = emptyList(),
    val selectedWorkspaceId: String? = null,
    val errorMessage: String? = null,
)