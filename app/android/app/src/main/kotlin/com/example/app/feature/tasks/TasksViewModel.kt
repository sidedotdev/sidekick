package com.example.app.feature.tasks

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.example.app.core.coroutine.DefaultDispatcherProvider
import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.DataStorePairingCredentialStore
import com.example.app.core.remote.DataStoreWorkspaceSelectionStore
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.RemoteSessionProvider
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.Workspace
import com.example.app.core.remote.WorkspaceSelectionStore
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

class TasksViewModel(
    private val hintedWorkspaceId: String?,
    private val credentialStore: PairingCredentialStore,
    private val workspaceSelectionStore: WorkspaceSelectionStore,
    private val remoteApiFactory: (PairingCredentials) -> SidekickRemoteApi,
    private val dispatchers: DispatcherProvider = DefaultDispatcherProvider(),
    private val remoteResources: AutoCloseable? = null,
) : ViewModel() {

    private val _uiState = MutableStateFlow(TasksUiState())
    val uiState: StateFlow<TasksUiState> = _uiState.asStateFlow()

    private var remoteApi: SidekickRemoteApi? = null
    private var workspacesJob: Job? = null
    private var tasksJob: Job? = null
    private var persistSelectionJob: Job? = null

    init {
        loadWorkspaces()
    }

    override fun onCleared() {
        remoteResources?.close()
    }

    fun onWorkspaceSelected(id: String) {
        val workspace = _uiState.value.workspaces.firstOrNull { it.id == id } ?: return
        if (workspace.id == _uiState.value.currentWorkspace?.id) {
            _uiState.update { it.copy(switcherQuery = "") }
            return
        }
        _uiState.update {
            it.copy(
                currentWorkspace = workspace,
                switcherQuery = "",
                tasks = emptyList(),
                tasksError = null,
            )
        }
        persistSelection(workspace.id)
        loadTasks(workspace.id, refresh = false)
    }

    fun onRefresh() {
        val workspace = _uiState.value.currentWorkspace
        if (workspace == null) {
            loadWorkspaces()
        } else {
            loadTasks(workspace.id, refresh = true)
        }
    }

    fun onRetryWorkspaces() {
        loadWorkspaces()
    }

    fun onRetryTasks() {
        _uiState.value.currentWorkspace?.let { loadTasks(it.id, refresh = false) }
    }

    fun onSearchQueryChanged(query: String) {
        _uiState.update { it.copy(searchQuery = query) }
    }

    fun onSearchActiveChanged(active: Boolean) {
        _uiState.update {
            it.copy(
                isSearchActive = active,
                searchQuery = if (active) it.searchQuery else "",
            )
        }
    }

    fun onToggleDrafts() {
        _uiState.update { it.copy(draftsExpanded = !it.draftsExpanded) }
    }

    fun onToggleDone() {
        _uiState.update { it.copy(doneExpanded = !it.doneExpanded) }
    }

    fun onSwitcherQueryChanged(query: String) {
        _uiState.update { it.copy(switcherQuery = query) }
    }

    private fun loadWorkspaces() {
        // Only the most recent load may decide the workspace list and selection.
        workspacesJob?.cancel()
        _uiState.update {
            it.copy(
                isLoadingWorkspaces = true,
                workspacesError = null,
            )
        }
        workspacesJob = viewModelScope.launch(dispatchers.io) {
            try {
                val api = resolveRemoteApi() ?: return@launch
                val workspaces = api.getWorkspaces().workspaces
                val persistedId = readPersistedSelection()
                val selected = pickWorkspace(
                    workspaces = workspaces,
                    preferredIds = listOf(_uiState.value.currentWorkspace?.id, persistedId, hintedWorkspaceId),
                )
                _uiState.update {
                    val workspaceChanged = selected?.id != it.currentWorkspace?.id
                    it.copy(
                        isLoadingWorkspaces = false,
                        workspaces = workspaces,
                        workspacesError = null,
                        currentWorkspace = selected,
                        tasks = if (workspaceChanged) emptyList() else it.tasks,
                    )
                }
                if (selected == null) {
                    clearTaskLoading()
                } else {
                    if (selected.id != persistedId) {
                        persistSelection(selected.id)
                    }
                    loadTasks(selected.id, refresh = false)
                }
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                _uiState.update {
                    it.copy(
                        isLoadingWorkspaces = false,
                        workspacesError = "Workspaces could not be loaded.",
                    )
                }
            }
        }
    }

    private fun loadTasks(workspaceId: String, refresh: Boolean) {
        tasksJob?.cancel()
        _uiState.update {
            it.copy(
                isLoadingTasks = !refresh,
                isRefreshing = refresh,
                tasksError = null,
            )
        }
        tasksJob = viewModelScope.launch(dispatchers.io) {
            try {
                val api = resolveRemoteApi() ?: return@launch
                val tasks = api.getTasks(workspaceId).tasks
                _uiState.update {
                    // A stale response for a workspace the user has since left must not win.
                    if (it.currentWorkspace?.id != workspaceId) {
                        it
                    } else {
                        it.copy(
                            isLoadingTasks = false,
                            isRefreshing = false,
                            tasks = tasks,
                            tasksError = null,
                        )
                    }
                }
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                _uiState.update {
                    if (it.currentWorkspace?.id != workspaceId) {
                        it
                    } else {
                        it.copy(
                            isLoadingTasks = false,
                            isRefreshing = false,
                            tasksError = "Tasks could not be loaded.",
                        )
                    }
                }
            }
        }
    }

    private fun clearTaskLoading() {
        tasksJob?.cancel()
        _uiState.update {
            it.copy(
                tasks = emptyList(),
                isLoadingTasks = false,
                isRefreshing = false,
                tasksError = null,
            )
        }
    }

    private suspend fun resolveRemoteApi(): SidekickRemoteApi? {
        remoteApi?.let { return it }
        // Unreadable credentials are treated like absent ones: the remedy is re-pairing, not retrying.
        val credentials = try {
            credentialStore.credentials.first()
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            null
        }
        if (credentials == null) {
            _uiState.update {
                it.copy(
                    credentialsMissing = true,
                    isLoadingWorkspaces = false,
                    isLoadingTasks = false,
                    isRefreshing = false,
                )
            }
            return null
        }
        _uiState.update { it.copy(credentialsMissing = false) }
        return remoteApiFactory(credentials).also { remoteApi = it }
    }

    private suspend fun readPersistedSelection(): String? = try {
        workspaceSelectionStore.lastWorkspaceId.first()
    } catch (error: CancellationException) {
        throw error
    } catch (_: Exception) {
        null
    }

    private fun persistSelection(workspaceId: String) {
        // Abandon any in-flight save for an earlier pick so saves cannot land out of order.
        persistSelectionJob?.cancel()
        persistSelectionJob = viewModelScope.launch(dispatchers.io) {
            try {
                workspaceSelectionStore.save(workspaceId)
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                // Failing to remember the selection must not prevent showing tasks.
            }
        }
    }

    private fun pickWorkspace(
        workspaces: List<Workspace>,
        preferredIds: List<String?>,
    ): Workspace? {
        for (id in preferredIds) {
            if (id == null) continue
            workspaces.firstOrNull { it.id == id }?.let { return it }
        }
        return workspaces.firstOrNull()
    }
}

class TasksViewModelFactory(
    context: Context,
    private val hintedWorkspaceId: String?,
) : ViewModelProvider.Factory {
    private val applicationContext = context.applicationContext

    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        require(modelClass.isAssignableFrom(TasksViewModel::class.java))
        val sessions = RemoteSessionProvider()
        return TasksViewModel(
            hintedWorkspaceId = hintedWorkspaceId,
            credentialStore = DataStorePairingCredentialStore(applicationContext),
            workspaceSelectionStore = DataStoreWorkspaceSelectionStore(applicationContext),
            remoteApiFactory = sessions::apiFor,
            remoteResources = sessions,
        ) as T
    }
}