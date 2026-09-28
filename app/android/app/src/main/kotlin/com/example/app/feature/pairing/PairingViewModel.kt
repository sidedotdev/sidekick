package com.example.app.feature.pairing

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.example.app.core.coroutine.DefaultDispatcherProvider
import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.DataStorePairingCredentialStore
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingPayloadParser
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * Restores or saves pairing credentials. Workspace discovery happens on the Tasks screen.
 *
 * [restoreStoredPairing] is false when the user deliberately came here to scan a
 * different code, so existing credentials must not send them straight back to Tasks.
 */
class PairingViewModel(
    private val credentialStore: PairingCredentialStore,
    private val payloadParser: PairingPayloadParser = PairingPayloadParser(),
    restoreStoredPairing: Boolean = true,
    private val dispatchers: DispatcherProvider = DefaultDispatcherProvider(),
    private val remoteResources: AutoCloseable? = null,
) : ViewModel() {

    private val _uiState = MutableStateFlow(PairingUiState(isCheckingStoredPairing = restoreStoredPairing))
    val uiState: StateFlow<PairingUiState> = _uiState.asStateFlow()

    init {
        if (restoreStoredPairing) {
            viewModelScope.launch(dispatchers.io) {
                restorePairing()
            }
        }
    }

    override fun onCleared() {
        remoteResources?.close()
    }

    fun onPairingPayload(payload: String) {
        val parsedCredentials = try {
            payloadParser.parse(payload)
        } catch (_: Exception) {
            _uiState.update {
                it.copy(
                    isCheckingStoredPairing = false,
                    isPaired = false,
                    errorMessage = "This QR code is not a valid Sidekick pairing code.",
                )
            }
            return
        }

        viewModelScope.launch(dispatchers.io) {
            try {
                credentialStore.save(parsedCredentials)
                _uiState.update {
                    it.copy(
                        isCheckingStoredPairing = false,
                        isPaired = true,
                        hintedWorkspaceId = parsedCredentials.workspaceId,
                        errorMessage = null,
                    )
                }
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                _uiState.update {
                    it.copy(
                        isCheckingStoredPairing = false,
                        isPaired = false,
                        errorMessage = "Pairing could not be saved. Scan the code again.",
                    )
                }
            }
        }
    }

    private suspend fun restorePairing() {
        try {
            val storedCredentials = credentialStore.credentials.first()
            _uiState.update {
                it.copy(
                    isCheckingStoredPairing = false,
                    isPaired = storedCredentials != null,
                    hintedWorkspaceId = storedCredentials?.workspaceId,
                    errorMessage = null,
                )
            }
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            _uiState.update {
                it.copy(
                    isCheckingStoredPairing = false,
                    isPaired = false,
                    errorMessage = "Saved pairing details could not be read.",
                )
            }
        }
    }
}

class PairingViewModelFactory(
    context: Context,
    private val restoreStoredPairing: Boolean,
) : ViewModelProvider.Factory {
    private val applicationContext = context.applicationContext

    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        require(modelClass.isAssignableFrom(PairingViewModel::class.java))
        return PairingViewModel(
            credentialStore = DataStorePairingCredentialStore(applicationContext),
            restoreStoredPairing = restoreStoredPairing,
        ) as T
    }
}