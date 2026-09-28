package com.example.app.feature.pairing

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.example.app.core.ui.theme.AppTheme
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions

/** Pairing entry point; whether stored credentials are restored is decided by the [viewModel]. */
@Composable
fun PairingRoute(
    viewModel: PairingViewModel,
    onPaired: (hintedWorkspaceId: String?) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val scanLauncher = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.let(viewModel::onPairingPayload)
    }

    PairingNavigationEffect(state = state, onPaired = onPaired)

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
        modifier = modifier,
    )
}

@Composable
fun PairingScreen(
    state: PairingUiState,
    onScan: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(modifier = modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.Center,
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            if (state.isCheckingStoredPairing || state.isPaired) {
                CircularProgressIndicator(modifier = Modifier.testTag("pairing-loading"))
                Spacer(modifier = Modifier.height(12.dp))
                Text(
                    text = if (state.isPaired) "Opening tasks…" else "Loading pairing…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                return@Column
            }

            Text(
                text = "Connect to Sidekick",
                style = MaterialTheme.typography.headlineSmall,
                textAlign = TextAlign.Center,
            )
            Spacer(modifier = Modifier.height(8.dp))
            Text(
                text = "Scan the pairing code shown by your Sidekick server to see its workspaces and tasks here.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                modifier = Modifier.widthIn(max = 320.dp),
            )
            Spacer(modifier = Modifier.height(20.dp))

            state.errorMessage?.let { message ->
                Text(
                    text = message,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.testTag("pairing-error"),
                )
                Spacer(modifier = Modifier.height(16.dp))
            }

            Button(
                onClick = onScan,
                modifier = Modifier.testTag("scan-pairing-code"),
            ) {
                Text("Scan pairing code")
            }
        }
    }
}

/** Hands off to the Tasks screen as soon as credentials are available. */
@Composable
fun PairingNavigationEffect(
    state: PairingUiState,
    onPaired: (hintedWorkspaceId: String?) -> Unit,
) {
    LaunchedEffect(state.isPaired) {
        if (state.isPaired) onPaired(state.hintedWorkspaceId)
    }
}

@Preview(name = "Pairing light", showBackground = true)
@Composable
private fun PairingScreenPreview() {
    AppTheme(darkTheme = false) {
        PairingScreen(state = PairingUiState(isCheckingStoredPairing = false), onScan = {})
    }
}

@Preview(name = "Pairing dark", showBackground = true)
@Composable
private fun PairingScreenDarkPreview() {
    AppTheme(darkTheme = true) {
        PairingScreen(
            state = PairingUiState(
                isCheckingStoredPairing = false,
                errorMessage = "This QR code is not a valid Sidekick pairing code.",
            ),
            onScan = {},
        )
    }
}