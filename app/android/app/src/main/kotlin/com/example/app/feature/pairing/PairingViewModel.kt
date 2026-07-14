package com.example.app.feature.pairing

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.example.app.core.coroutine.DefaultDispatcherProvider
import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.DataStorePairingCredentialStore
import com.example.app.core.remote.FfiIrohConnector
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.PairingPayloadParser
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.SidekickRemoteApiFactory
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

class PairingViewModel(
    private val credentialStore: PairingCredentialStore,
    private val payloadParser: PairingPayloadParser = PairingPayloadParser(),
    private val remoteApiFactory: (PairingCredentials) -> SidekickRemoteApi,
    private val dispatchers: DispatcherProvider = DefaultDispatcherProvider(),
) : ViewModel() {

    private val _uiState = MutableStateFlow(PairingUiState())
    val uiState: StateFlow<PairingUiState> = _uiState.asStateFlow()

    private var credentials: PairingCredentials? = null

    init {
        viewModelScope.launch(dispatchers.io) {
            restorePairing()
        }
    }

    fun onPairingPayload(payload: String) {
        val parsedCredentials = try {
            payloadParser.parse(payload)
        } catch (_: Exception) {
            _uiState.update {
                it.copy(
                    isPaired = false,
                    isLoading = false,
                    workspaces = emptyList(),
                    selectedWorkspaceId = null,
                    errorMessage = "This QR code is not a valid Sidekick pairing code.",
                )
            }
            return
        }

        _uiState.update {
            it.copy(
                isPaired = true,
                isLoading = true,
                workspaces = emptyList(),
                selectedWorkspaceId = null,
                errorMessage = null,
            )
        }

        viewModelScope.launch(dispatchers.io) {
            try {
                credentialStore.save(parsedCredentials)
                credentials = parsedCredentials
                fetchWorkspaces(parsedCredentials)
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                credentials = null
                _uiState.update {
                    it.copy(
                        isPaired = false,
                        isLoading = false,
                        workspaces = emptyList(),
                        selectedWorkspaceId = null,
                        errorMessage = "Pairing could not be saved. Scan the code again.",
                    )
                }
            }
        }
    }

    fun onRetry() {
        val currentCredentials = credentials ?: return

        _uiState.update {
            it.copy(
                isLoading = true,
                errorMessage = null,
            )
        }
        viewModelScope.launch(dispatchers.io) {
            fetchWorkspaces(currentCredentials)
        }
    }

    fun onWorkspaceSelected(workspaceId: String) {
        if (_uiState.value.workspaces.none { it.id == workspaceId }) return
        _uiState.update { it.copy(selectedWorkspaceId = workspaceId) }
    }

    private suspend fun restorePairing() {
        try {
            val storedCredentials = credentialStore.credentials.first()
            if (storedCredentials == null) {
                _uiState.update {
                    it.copy(
                        isPaired = false,
                        isLoading = false,
                        errorMessage = null,
                    )
                }
                return
            }

            credentials = storedCredentials
            _uiState.update {
                it.copy(
                    isPaired = true,
                    isLoading = true,
                    errorMessage = null,
                )
            }
            fetchWorkspaces(storedCredentials)
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            _uiState.update {
                it.copy(
                    isPaired = false,
                    isLoading = false,
                    errorMessage = "Saved pairing details could not be read.",
                )
            }
        }
    }

    private suspend fun fetchWorkspaces(credentials: PairingCredentials) {
        try {
            val workspaces = remoteApiFactory(credentials).getWorkspaces().workspaces
            _uiState.update { state ->
                state.copy(
                    isPaired = true,
                    isLoading = false,
                    workspaces = workspaces,
                    selectedWorkspaceId = state.selectedWorkspaceId
                        ?.takeIf { selectedId -> workspaces.any { it.id == selectedId } },
                    errorMessage = null,
                )
            }
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            _uiState.update {
                it.copy(
                    isPaired = true,
                    isLoading = false,
                    workspaces = emptyList(),
                    selectedWorkspaceId = null,
                    errorMessage = "Workspaces could not be loaded. Check the connection and try again.",
                )
            }
        }
    }
}

class PairingViewModelFactory(
    context: Context,
) : ViewModelProvider.Factory {
    private val applicationContext = context.applicationContext

    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        require(modelClass.isAssignableFrom(PairingViewModel::class.java))
        return PairingViewModel(
            credentialStore = DataStorePairingCredentialStore(applicationContext),
            remoteApiFactory = { credentials ->
                SidekickRemoteApiFactory().create(
                    credentials = credentials,
                    connector = FfiIrohConnector(),
                )
            },
        ) as T
    }
}