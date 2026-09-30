package com.example.app

import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.StubSidekickRemoteApi
import com.example.app.core.remote.Task
import com.example.app.core.remote.TaskListResponse
import com.example.app.core.remote.Workspace
import com.example.app.core.remote.WorkspaceListResponse
import com.example.app.core.remote.WorkspaceSelectionStore
import com.example.app.feature.tasks.TasksViewModel
import androidx.lifecycle.ViewModelStore
import java.io.IOException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class TasksViewModelTest {

    private val alpha = Workspace(id = "alpha", name = "Alpha")
    private val beta = Workspace(id = "beta", name = "Beta Backend")
    private val gamma = Workspace(id = "gamma", name = "Gamma")

    @Test
    fun `persisted workspace wins over hint and first workspace`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha, beta, gamma),
            tasksByWorkspace = mapOf("gamma" to listOf(task("g1", "gamma"))),
        )
        val selectionStore = FakeWorkspaceSelectionStore(initialId = "gamma")
        val viewModel = createViewModel(api, hintedWorkspaceId = "beta", selectionStore = selectionStore)

        assertTrue(viewModel.uiState.value.isLoadingWorkspaces)

        advanceUntilIdle()

        val state = viewModel.uiState.value
        assertFalse(state.isLoadingWorkspaces)
        assertFalse(state.isLoadingTasks)
        assertEquals(gamma, state.currentWorkspace)
        assertEquals(listOf(alpha, beta, gamma), state.workspaces)
        assertEquals(listOf("g1"), state.tasks.map { it.id })
        assertEquals(listOf("gamma"), api.requestedWorkspaceIds)
        assertEquals("gamma", selectionStore.lastWorkspaceId.value)
    }

    @Test
    fun `hinted workspace is used when nothing is persisted and the pick is persisted`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha, beta, gamma))
        val selectionStore = FakeWorkspaceSelectionStore()
        val viewModel = createViewModel(api, hintedWorkspaceId = "beta", selectionStore = selectionStore)

        advanceUntilIdle()

        assertEquals(beta, viewModel.uiState.value.currentWorkspace)
        assertEquals(listOf("beta"), api.requestedWorkspaceIds)
        assertEquals(listOf("beta"), selectionStore.savedIds)
    }

    @Test
    fun `first workspace is used when persisted and hinted ids are not listed`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha, beta))
        val selectionStore = FakeWorkspaceSelectionStore(initialId = "deleted")
        val viewModel = createViewModel(api, hintedWorkspaceId = "unknown", selectionStore = selectionStore)

        advanceUntilIdle()

        assertEquals(alpha, viewModel.uiState.value.currentWorkspace)
        assertEquals(listOf("alpha"), api.requestedWorkspaceIds)
        assertEquals("alpha", selectionStore.lastWorkspaceId.value)
    }

    @Test
    fun `selecting a workspace persists it and reloads tasks while keeping the workspace shown`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha, beta),
            tasksByWorkspace = mapOf(
                "alpha" to listOf(task("a1", "alpha")),
                "beta" to listOf(task("b1", "beta"), task("b2", "beta")),
            ),
        )
        val selectionStore = FakeWorkspaceSelectionStore()
        val viewModel = createViewModel(api, selectionStore = selectionStore)
        advanceUntilIdle()
        assertEquals(listOf("a1"), viewModel.uiState.value.tasks.map { it.id })

        viewModel.onWorkspaceSelected("beta")

        val loading = viewModel.uiState.value
        assertEquals(beta, loading.currentWorkspace)
        assertTrue(loading.isLoadingTasks)
        assertTrue(loading.tasks.isEmpty())
        assertFalse(loading.isLoadingWorkspaces)

        advanceUntilIdle()

        val loaded = viewModel.uiState.value
        assertEquals(beta, loaded.currentWorkspace)
        assertFalse(loaded.isLoadingTasks)
        assertEquals(listOf("b1", "b2"), loaded.tasks.map { it.id })
        assertEquals(listOf("alpha", "beta"), api.requestedWorkspaceIds)
        assertEquals("beta", selectionStore.lastWorkspaceId.value)
    }

    @Test
    fun `selecting an unknown workspace is ignored`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha))
        val selectionStore = FakeWorkspaceSelectionStore()
        val viewModel = createViewModel(api, selectionStore = selectionStore)
        advanceUntilIdle()

        viewModel.onWorkspaceSelected("missing")
        advanceUntilIdle()

        assertEquals(alpha, viewModel.uiState.value.currentWorkspace)
        assertEquals(listOf("alpha"), api.requestedWorkspaceIds)
        assertEquals("alpha", selectionStore.lastWorkspaceId.value)
    }

    @Test
    fun `refresh reloads tasks with the refreshing flag instead of the loading state`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha),
            tasksByWorkspace = mapOf("alpha" to listOf(task("a1", "alpha"))),
        )
        val viewModel = createViewModel(api)
        advanceUntilIdle()

        api.tasksByWorkspace = mapOf("alpha" to listOf(task("a1", "alpha"), task("a2", "alpha")))
        viewModel.onRefresh()

        val refreshing = viewModel.uiState.value
        assertTrue(refreshing.isRefreshing)
        assertFalse(refreshing.isLoadingTasks)
        assertEquals(listOf("a1"), refreshing.tasks.map { it.id })

        advanceUntilIdle()

        val refreshed = viewModel.uiState.value
        assertFalse(refreshed.isRefreshing)
        assertEquals(listOf("a1", "a2"), refreshed.tasks.map { it.id })
        assertEquals(listOf("alpha", "alpha"), api.requestedWorkspaceIds)
    }

    @Test
    fun `workspace load failure exposes an error and retry recovers`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha),
            workspacesError = IOException("offline"),
            tasksByWorkspace = mapOf("alpha" to listOf(task("a1", "alpha"))),
        )
        val viewModel = createViewModel(api)

        advanceUntilIdle()

        val failed = viewModel.uiState.value
        assertFalse(failed.isLoadingWorkspaces)
        assertEquals("Workspaces could not be loaded.", failed.workspacesError)
        assertEquals("IOException: offline", failed.workspacesErrorDetail)
        assertNull(failed.currentWorkspace)
        assertTrue(api.requestedWorkspaceIds.isEmpty())

        api.workspacesError = null
        viewModel.onRetryWorkspaces()

        assertTrue(viewModel.uiState.value.isLoadingWorkspaces)
        assertNull(viewModel.uiState.value.workspacesError)
        assertNull(viewModel.uiState.value.workspacesErrorDetail)

        advanceUntilIdle()

        val recovered = viewModel.uiState.value
        assertFalse(recovered.isLoadingWorkspaces)
        assertNull(recovered.workspacesError)
        assertEquals(alpha, recovered.currentWorkspace)
        assertEquals(listOf("a1"), recovered.tasks.map { it.id })
    }

    @Test
    fun `task load failure exposes an error and retry recovers`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha),
            tasksError = IOException("offline"),
        )
        val viewModel = createViewModel(api)

        advanceUntilIdle()

        val failed = viewModel.uiState.value
        assertEquals(alpha, failed.currentWorkspace)
        assertFalse(failed.isLoadingTasks)
        assertEquals("Tasks could not be loaded.", failed.tasksError)
        assertEquals("IOException: offline", failed.tasksErrorDetail)
        assertNull(failed.workspacesError)

        api.tasksError = null
        api.tasksByWorkspace = mapOf("alpha" to listOf(task("retry", "alpha")))
        viewModel.onRetryTasks()

        assertTrue(viewModel.uiState.value.isLoadingTasks)
        assertNull(viewModel.uiState.value.tasksError)
        assertNull(viewModel.uiState.value.tasksErrorDetail)

        advanceUntilIdle()

        val recovered = viewModel.uiState.value
        assertFalse(recovered.isLoadingTasks)
        assertNull(recovered.tasksError)
        assertEquals(listOf("retry"), recovered.tasks.map { it.id })
        assertEquals(2, api.requestedWorkspaceIds.size)
    }

    @Test
    fun `error details chain causes and never quote pairing credentials`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha),
            tasksError = IOException(
                "failed to connect to proxy for ticket secret-ticket",
                java.net.SocketTimeoutException("Bearer secret-token timed out"),
            ),
        )
        val reported = mutableListOf<String>()
        val viewModel = createViewModel(
            api,
            credentials = PairingCredentials("secret-ticket", "secret-token"),
            onFailure = { message -> reported += message },
        )

        advanceUntilIdle()

        val detail = viewModel.uiState.value.tasksErrorDetail
        assertEquals(
            "IOException: failed to connect to proxy for ticket <redacted> <- SocketTimeoutException: Bearer <redacted> timed out",
            detail,
        )
        assertEquals(
            listOf("task load failed for workspace alpha: $detail"),
            reported,
        )
        assertFalse(reported.single().contains("secret-"))
    }

    @Test
    fun `missing credentials are flagged without calling the api`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha))
        val viewModel = createViewModel(api, credentials = null)

        advanceUntilIdle()

        val state = viewModel.uiState.value
        assertTrue(state.credentialsMissing)
        assertFalse(state.isLoadingWorkspaces)
        assertNull(state.workspacesError)
        assertEquals(0, api.workspaceRequests)
        assertTrue(api.requestedWorkspaceIds.isEmpty())
    }

    @Test
    fun `unreadable credentials fall back to pairing and recovery clears the flag`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha),
            tasksByWorkspace = mapOf("alpha" to listOf(task("a1", "alpha"))),
        )
        val credentialStore = FakeCredentialStore(
            initialCredentials = PairingCredentials("ticket", "token"),
            readError = IOException("unreadable"),
        )
        val viewModel = createViewModel(api, credentialStore = credentialStore)

        advanceUntilIdle()

        val failed = viewModel.uiState.value
        assertTrue(failed.credentialsMissing)
        assertFalse(failed.isLoadingWorkspaces)
        assertNull(failed.workspacesError)
        assertEquals(0, api.workspaceRequests)

        credentialStore.readError = null
        viewModel.onRetryWorkspaces()
        advanceUntilIdle()

        val recovered = viewModel.uiState.value
        assertFalse(recovered.credentialsMissing)
        assertEquals(alpha, recovered.currentWorkspace)
        assertEquals(listOf("a1"), recovered.tasks.map { it.id })
    }

    @Test
    fun `a superseded workspace load cannot overwrite a newer one`() = runTest {
        val api = FakeRemoteApi(
            deferWorkspaces = true,
            tasksByWorkspace = mapOf("alpha" to listOf(task("a1", "alpha"))),
        )
        val viewModel = createViewModel(api)
        advanceUntilIdle()
        val staleRequest = api.pendingWorkspaceRequests.single()

        viewModel.onRetryWorkspaces()
        advanceUntilIdle()
        val latestRequest = api.pendingWorkspaceRequests.last()
        assertEquals(2, api.pendingWorkspaceRequests.size)

        latestRequest.complete(WorkspaceListResponse(listOf(alpha, beta)))
        advanceUntilIdle()
        assertEquals(alpha, viewModel.uiState.value.currentWorkspace)

        staleRequest.complete(WorkspaceListResponse(listOf(gamma)))
        advanceUntilIdle()

        val state = viewModel.uiState.value
        assertEquals(listOf(alpha, beta), state.workspaces)
        assertEquals(alpha, state.currentWorkspace)
        assertEquals(listOf("a1"), state.tasks.map { it.id })
        assertEquals(listOf("alpha"), api.requestedWorkspaceIds)
    }

    @Test
    fun `a reload that no longer lists any workspace clears the shown tasks`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha),
            tasksByWorkspace = mapOf("alpha" to listOf(task("a1", "alpha"))),
        )
        val viewModel = createViewModel(api)
        advanceUntilIdle()
        assertEquals(listOf("a1"), viewModel.uiState.value.tasks.map { it.id })

        api.workspaces = emptyList()
        viewModel.onRetryWorkspaces()
        advanceUntilIdle()

        val state = viewModel.uiState.value
        assertTrue(state.workspaces.isEmpty())
        assertNull(state.currentWorkspace)
        assertTrue(state.tasks.isEmpty())
        assertFalse(state.isLoadingTasks)
        assertFalse(state.isRefreshing)
        assertNull(state.tasksError)
        assertEquals(listOf("alpha"), api.requestedWorkspaceIds)
    }

    @Test
    fun `a reload that changes the selected workspace drops the previous tasks while loading`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha, beta),
            tasksByWorkspace = mapOf(
                "alpha" to listOf(task("a1", "alpha")),
                "beta" to listOf(task("b1", "beta")),
            ),
        )
        val viewModel = createViewModel(api)
        advanceUntilIdle()

        api.workspaces = listOf(beta)
        api.deferTasks = true
        viewModel.onRetryWorkspaces()
        advanceUntilIdle()

        val loading = viewModel.uiState.value
        assertEquals(beta, loading.currentWorkspace)
        assertTrue(loading.tasks.isEmpty())
        assertTrue(loading.isLoadingTasks)

        api.pendingTaskRequests.single().second.complete(TaskListResponse(listOf(task("b1", "beta"))))
        advanceUntilIdle()

        assertEquals(listOf("b1"), viewModel.uiState.value.tasks.map { it.id })
    }

    @Test
    fun `a late task response for a previous workspace is ignored`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha, beta), deferTasks = true)
        val viewModel = createViewModel(api)
        advanceUntilIdle()

        viewModel.onWorkspaceSelected("beta")
        advanceUntilIdle()
        val (alphaId, alphaResponse) = api.pendingTaskRequests[0]
        val (betaId, betaResponse) = api.pendingTaskRequests[1]
        assertEquals("alpha", alphaId)
        assertEquals("beta", betaId)

        alphaResponse.complete(TaskListResponse(listOf(task("a1", "alpha"))))
        advanceUntilIdle()

        assertTrue(viewModel.uiState.value.tasks.isEmpty())
        assertTrue(viewModel.uiState.value.isLoadingTasks)

        betaResponse.complete(TaskListResponse(listOf(task("b1", "beta"))))
        advanceUntilIdle()

        assertEquals(listOf("b1"), viewModel.uiState.value.tasks.map { it.id })
        assertFalse(viewModel.uiState.value.isLoadingTasks)
    }

    @Test
    fun `rapid selections persist the last pick even when saves finish out of order`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha, beta, gamma))
        val selectionStore = FakeWorkspaceSelectionStore(initialId = "alpha", deferSaves = true)
        val viewModel = createViewModel(api, selectionStore = selectionStore)
        advanceUntilIdle()
        assertTrue(selectionStore.pendingSaves.isEmpty())

        viewModel.onWorkspaceSelected("beta")
        advanceUntilIdle()
        viewModel.onWorkspaceSelected("gamma")
        advanceUntilIdle()

        val saves = selectionStore.pendingSaves.toList()
        assertEquals(listOf("beta", "gamma"), saves.map { it.first })
        saves.first { it.first == "gamma" }.second.complete(Unit)
        saves.first { it.first == "beta" }.second.complete(Unit)
        advanceUntilIdle()

        assertEquals("gamma", selectionStore.lastWorkspaceId.value)
        assertEquals(gamma, viewModel.uiState.value.currentWorkspace)
    }

    @Test
    fun `remote factory receives stored credentials`() = runTest {
        val credentials = PairingCredentials("ticket", "token")
        val api = FakeRemoteApi(workspaces = listOf(alpha))
        createViewModel(api, credentials = credentials)

        advanceUntilIdle()

        assertEquals(credentials, api.receivedCredentials)
    }

    @Test
    fun `empty workspace list completes without a current workspace or task request`() = runTest {
        val api = FakeRemoteApi()
        val selectionStore = FakeWorkspaceSelectionStore()
        val viewModel = createViewModel(api, hintedWorkspaceId = "beta", selectionStore = selectionStore)

        advanceUntilIdle()

        val state = viewModel.uiState.value
        assertFalse(state.isLoadingWorkspaces)
        assertFalse(state.isLoadingTasks)
        assertNull(state.workspacesError)
        assertNull(state.currentWorkspace)
        assertTrue(state.workspaces.isEmpty())
        assertTrue(api.requestedWorkspaceIds.isEmpty())
        assertTrue(selectionStore.savedIds.isEmpty())
    }

    @Test
    fun `search and switcher queries drive the derived lists`() = runTest {
        val api = FakeRemoteApi(
            workspaces = listOf(alpha, beta, gamma),
            tasksByWorkspace = mapOf(
                "alpha" to listOf(
                    task("draft", "alpha", title = "Draft deploy notes", status = "drafting"),
                    task("review", "alpha", title = "Review deploy", status = "in_review"),
                    task("other", "alpha", title = "Unrelated", status = "to_do"),
                ),
            ),
        )
        val viewModel = createViewModel(api)
        advanceUntilIdle()

        viewModel.onSearchActiveChanged(true)
        viewModel.onSearchQueryChanged("DEPLOY")

        val searching = viewModel.uiState.value
        assertTrue(searching.isSearchActive)
        assertEquals(listOf("review", "draft"), searching.searchResults.map { it.id })
        assertEquals(listOf("review"), searching.bucketed.needsAttention.map { it.id })

        viewModel.onSearchActiveChanged(false)

        assertEquals("", viewModel.uiState.value.searchQuery)
        assertEquals(3, viewModel.uiState.value.searchResults.size)

        viewModel.onSwitcherQueryChanged("bAck")

        assertEquals(listOf(beta), viewModel.uiState.value.filteredWorkspaces)

        viewModel.onToggleDrafts()
        viewModel.onToggleDone()

        assertTrue(viewModel.uiState.value.draftsExpanded)
        assertTrue(viewModel.uiState.value.doneExpanded)
    }

    @Test
    fun `clearing the view model releases the remote session resources`() = runTest {
        val api = FakeRemoteApi(workspaces = listOf(alpha), tasksByWorkspace = mapOf("alpha" to emptyList()))
        var closeCount = 0
        val store = ViewModelStore()
        val viewModel = createViewModel(api, remoteResources = AutoCloseable { closeCount++ })
        store.put("tasks", viewModel)
        advanceUntilIdle()

        assertEquals(0, closeCount)

        store.clear()

        assertEquals(1, closeCount)
    }

    private fun TestScope.createViewModel(
        api: FakeRemoteApi,
        hintedWorkspaceId: String? = null,
        selectionStore: FakeWorkspaceSelectionStore = FakeWorkspaceSelectionStore(),
        credentials: PairingCredentials? = PairingCredentials("ticket", "token"),
        credentialStore: FakeCredentialStore = FakeCredentialStore(initialCredentials = credentials),
        remoteResources: AutoCloseable? = null,
        onFailure: (String) -> Unit = {},
    ) = TasksViewModel(
        hintedWorkspaceId = hintedWorkspaceId,
        credentialStore = credentialStore,
        workspaceSelectionStore = selectionStore,
        remoteApiFactory = { receivedCredentials ->
            api.receivedCredentials = receivedCredentials
            api
        },
        dispatchers = TestDispatcherProvider(StandardTestDispatcher(testScheduler)),
        remoteResources = remoteResources,
        onFailure = onFailure,
    )

    private fun TestScope.advanceUntilIdle() = testScheduler.advanceUntilIdle()

    private fun task(
        id: String,
        workspaceId: String,
        title: String = "Task $id",
        status: String = "to_do",
    ) = Task(
        id = id,
        workspaceId = workspaceId,
        title = title,
        status = status,
    )

    private class FakeCredentialStore(
        initialCredentials: PairingCredentials?,
        var readError: Throwable? = null,
    ) : PairingCredentialStore {
        private val credentialFlow = MutableStateFlow(initialCredentials)

        override val credentials: Flow<PairingCredentials?> = flow {
            readError?.let { throw it }
            emitAll(credentialFlow)
        }

        override suspend fun save(credentials: PairingCredentials) {
            credentialFlow.value = credentials
        }

        override suspend fun clear() {
            credentialFlow.value = null
        }
    }

    private class FakeWorkspaceSelectionStore(
        initialId: String? = null,
        private val deferSaves: Boolean = false,
    ) : WorkspaceSelectionStore {
        override val lastWorkspaceId = MutableStateFlow(initialId)
        val savedIds = mutableListOf<String>()
        val pendingSaves = mutableListOf<Pair<String, CompletableDeferred<Unit>>>()

        override suspend fun save(id: String) {
            if (deferSaves) {
                val gate = CompletableDeferred<Unit>()
                pendingSaves += id to gate
                gate.await()
            }
            savedIds += id
            lastWorkspaceId.value = id
        }

        override suspend fun clear() {
            lastWorkspaceId.value = null
        }
    }

    private class FakeRemoteApi(
        var workspaces: List<Workspace> = emptyList(),
        var workspacesError: Throwable? = null,
        var tasksByWorkspace: Map<String, List<Task>> = emptyMap(),
        var tasksError: Throwable? = null,
        var deferWorkspaces: Boolean = false,
        var deferTasks: Boolean = false,
    ) : StubSidekickRemoteApi() {
        var workspaceRequests = 0
        val requestedWorkspaceIds = mutableListOf<String>()
        val pendingWorkspaceRequests = mutableListOf<CompletableDeferred<WorkspaceListResponse>>()
        val pendingTaskRequests = mutableListOf<Pair<String, CompletableDeferred<TaskListResponse>>>()
        var receivedCredentials: PairingCredentials? = null

        override suspend fun getWorkspaces(): WorkspaceListResponse {
            workspaceRequests++
            if (deferWorkspaces) {
                val response = CompletableDeferred<WorkspaceListResponse>()
                pendingWorkspaceRequests += response
                return response.await()
            }
            workspacesError?.let { throw it }
            return WorkspaceListResponse(workspaces)
        }

        override suspend fun getTasks(workspaceId: String): TaskListResponse {
            requestedWorkspaceIds += workspaceId
            if (deferTasks) {
                val response = CompletableDeferred<TaskListResponse>()
                pendingTaskRequests += workspaceId to response
                return response.await()
            }
            tasksError?.let { throw it }
            return TaskListResponse(tasksByWorkspace[workspaceId].orEmpty())
        }
    }

    private class TestDispatcherProvider(
        dispatcher: CoroutineDispatcher,
    ) : DispatcherProvider {
        override val default: CoroutineDispatcher = dispatcher
        override val io: CoroutineDispatcher = dispatcher
        override val main: CoroutineDispatcher = dispatcher
    }
}
