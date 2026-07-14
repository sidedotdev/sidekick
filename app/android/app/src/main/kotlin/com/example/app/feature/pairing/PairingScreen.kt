package com.example.app.feature.pairing

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.example.app.core.remote.Workspace
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions

@Composable
fun PairingRoute(
    onWorkspaceSelected: (Workspace) -> Unit = {},
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val factory = remember(context) { PairingViewModelFactory(context) }
    val viewModel: PairingViewModel = viewModel(factory = factory)
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val scanLauncher = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.let(viewModel::onPairingPayload)
    }

    PairingScreen(
        state = state,
        onScan = {
            scanLauncher.launch(
                ScanOptions()
                    .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                    .setPrompt("Scan a Sidekick pairing code")
                    .setBeepEnabled(false)
                    .setOrientationLocked(false),
            )
        },
        onRetry = viewModel::onRetry,
        onWorkspaceSelected = { workspace ->
            viewModel.onWorkspaceSelected(workspace.id)
            onWorkspaceSelected(workspace)
        },
        modifier = modifier,
    )
}

@Composable
fun PairingScreen(
    state: PairingUiState,
    onScan: () -> Unit,
    onRetry: () -> Unit,
    onWorkspaceSelected: (Workspace) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = if (state.isPaired) "Choose a workspace" else "Connect to Sidekick",
            style = MaterialTheme.typography.headlineMedium,
        )
        Spacer(modifier = Modifier.height(16.dp))

        if (state.isLoading) {
            CircularProgressIndicator(modifier = Modifier.testTag("pairing-loading"))
            Spacer(modifier = Modifier.height(12.dp))
            Text(if (state.isPaired) "Loading workspaces…" else "Loading pairing…")
            return@Column
        }

        state.errorMessage?.let { message ->
            Text(
                text = message,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.testTag("pairing-error"),
            )
            Spacer(modifier = Modifier.height(16.dp))
        }

        if (!state.isPaired) {
            Button(
                onClick = onScan,
                modifier = Modifier.testTag("scan-pairing-code"),
            ) {
                Text("Scan pairing code")
            }
            return@Column
        }

        if (state.errorMessage != null) {
            Button(
                onClick = onRetry,
                modifier = Modifier.testTag("retry-workspaces"),
            ) {
                Text("Retry")
            }
            Spacer(modifier = Modifier.height(8.dp))
            OutlinedButton(
                onClick = onScan,
                modifier = Modifier.testTag("scan-different-code"),
            ) {
                Text("Scan a different code")
            }
            return@Column
        }

        if (state.workspaces.isEmpty()) {
            Text(
                text = "No workspaces are available.",
                modifier = Modifier.testTag("empty-workspaces"),
            )
            return@Column
        }

        Column(
            modifier = Modifier.fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            state.workspaces.forEach { workspace ->
                val isSelected = workspace.id == state.selectedWorkspaceId
                OutlinedButton(
                    onClick = { onWorkspaceSelected(workspace) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .semantics { selected = isSelected }
                        .testTag("workspace-${workspace.id}"),
                ) {
                    Text(workspace.name)
                }
            }
        }
    }
}