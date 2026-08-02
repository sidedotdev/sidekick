package com.example.app

import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.Task
import com.example.app.core.remote.TaskListResponse
import com.example.app.core.remote.WorkspaceListResponse
import com.example.app.feature.tasks.TaskListViewModel
import java.io.IOException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class TaskListViewModelTest {

    @Test
    fun `tasks begin loading and populated response is exposed`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val api = FakeRemoteApi(
            tasks = listOf(
                task(id = "one", title = "First task", status = "in_progress"),
                task(id = "two", title = "Second task", status = "completed"),
            ),
        )
        val viewModel = createViewModel(api, dispatcher)

        assertTrue(viewModel.uiState.value.isLoading)

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertNull(viewModel.uiState.value.errorMessage)
        assertEquals(listOf("one", "two"), viewModel.uiState.value.tasks.map { it.id })
        assertEquals(listOf("workspace-1"), api.requestedWorkspaceIds)
    }

    @Test
    fun `empty response exposes an empty completed state`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val viewModel = createViewModel(FakeRemoteApi(), dispatcher)

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.tasks.isEmpty())
        assertNull(viewModel.uiState.value.errorMessage)
    }

    @Test
    fun `failed request exposes an error and retry can load tasks`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val api = FakeRemoteApi(error = IOException("offline"))
        val viewModel = createViewModel(api, dispatcher)

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.tasks.isEmpty())
        assertEquals("Tasks could not be loaded.", viewModel.uiState.value.errorMessage)

        api.error = null
        api.tasks = listOf(task(id = "retry", title = "Loaded after retry"))
        viewModel.onRetry()

        assertTrue(viewModel.uiState.value.isLoading)
        assertNull(viewModel.uiState.value.errorMessage)

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertNull(viewModel.uiState.value.errorMessage)
        assertEquals("retry", viewModel.uiState.value.tasks.single().id)
        assertEquals(2, api.requestedWorkspaceIds.size)
    }

    @Test
    fun `missing pairing credentials exposes an actionable error`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val api = FakeRemoteApi()
        val viewModel = createViewModel(
            api = api,
            dispatcher = dispatcher,
            credentials = null,
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.tasks.isEmpty())
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("pair again"))
        assertTrue(api.requestedWorkspaceIds.isEmpty())
    }

    @Test
    fun `credential read failure exposes an error without calling the api`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val api = FakeRemoteApi()
        val viewModel = createViewModel(
            api = api,
            dispatcher = dispatcher,
            credentialReadError = IOException("unreadable"),
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.tasks.isEmpty())
        assertEquals("Tasks could not be loaded.", viewModel.uiState.value.errorMessage)
        assertTrue(api.requestedWorkspaceIds.isEmpty())
    }

    @Test
    fun `remote factory receives stored credentials`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val credentials = PairingCredentials("ticket", "token")
        val api = FakeRemoteApi()
        val viewModel = createViewModel(
            api = api,
            dispatcher = dispatcher,
            credentials = credentials,
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(credentials, api.receivedCredentials)
    }

    private fun createViewModel(
        api: FakeRemoteApi,
        dispatcher: CoroutineDispatcher,
        credentials: PairingCredentials? = PairingCredentials("ticket", "token"),
        credentialReadError: Throwable? = null,
    ) = TaskListViewModel(
        workspaceId = "workspace-1",
        credentialStore = FakeCredentialStore(
            initialCredentials = credentials,
            readError = credentialReadError,
        ),
        remoteApiFactory = { receivedCredentials ->
            api.receivedCredentials = receivedCredentials
            api
        },
        dispatchers = TestDispatcherProvider(dispatcher),
    )

    private fun task(
        id: String,
        title: String,
        status: String = "todo",
    ) = Task(
        id = id,
        workspaceId = "workspace-1",
        title = title,
        status = status,
    )

    private class FakeCredentialStore(
        initialCredentials: PairingCredentials?,
        private val readError: Throwable? = null,
    ) : PairingCredentialStore {
        private val credentialFlow = MutableStateFlow(initialCredentials)

        override val credentials: Flow<PairingCredentials?> =
            if (readError == null) {
                credentialFlow
            } else {
                flow { throw checkNotNull(readError) }
            }

        override suspend fun save(credentials: PairingCredentials) {
            credentialFlow.value = credentials
        }

        override suspend fun clear() {
            credentialFlow.value = null
        }
    }

    private class FakeRemoteApi(
        var tasks: List<Task> = emptyList(),
        var error: Throwable? = null,
    ) : SidekickRemoteApi {
        val requestedWorkspaceIds = mutableListOf<String>()
        var receivedCredentials: PairingCredentials? = null

        override suspend fun getWorkspaces(): WorkspaceListResponse {
            return WorkspaceListResponse(emptyList())
        }

        override suspend fun getTasks(workspaceId: String): TaskListResponse {
            requestedWorkspaceIds += workspaceId
            error?.let { throw it }
            return TaskListResponse(tasks)
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