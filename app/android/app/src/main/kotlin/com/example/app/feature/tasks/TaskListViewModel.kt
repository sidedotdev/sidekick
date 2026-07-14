package com.example.app.feature.tasks

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
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.SidekickRemoteApiFactory
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

class TaskListViewModel(
    private val workspaceId: String,
    private val credentialStore: PairingCredentialStore,
    private val remoteApiFactory: (PairingCredentials) -> SidekickRemoteApi,
    private val dispatchers: DispatcherProvider = DefaultDispatcherProvider(),
) : ViewModel() {

    private val _uiState = MutableStateFlow(TaskListUiState())
    val uiState: StateFlow<TaskListUiState> = _uiState.asStateFlow()

    init {
        loadTasks()
    }

    fun onRetry() {
        loadTasks()
    }

    private fun loadTasks() {
        _uiState.update {
            it.copy(
                isLoading = true,
                errorMessage = null,
            )
        }
        viewModelScope.launch(dispatchers.io) {
            try {
                val credentials = credentialStore.credentials.first()
                if (credentials == null) {
                    _uiState.update {
                        it.copy(
                            isLoading = false,
                            tasks = emptyList(),
                            errorMessage = "Pairing details are unavailable. Return and pair again.",
                        )
                    }
                    return@launch
                }

                val tasks = remoteApiFactory(credentials).getTasks(workspaceId).tasks
                _uiState.update {
                    it.copy(
                        isLoading = false,
                        tasks = tasks,
                        errorMessage = null,
                    )
                }
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                _uiState.update {
                    it.copy(
                        isLoading = false,
                        tasks = emptyList(),
                        errorMessage = "Tasks could not be loaded.",
                    )
                }
            }
        }
    }
}

class TaskListViewModelFactory(
    context: Context,
    private val workspaceId: String,
) : ViewModelProvider.Factory {
    private val applicationContext = context.applicationContext

    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        require(modelClass.isAssignableFrom(TaskListViewModel::class.java))
        return TaskListViewModel(
            workspaceId = workspaceId,
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