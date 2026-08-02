package com.example.app

import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.TaskListResponse
import com.example.app.core.remote.Workspace
import com.example.app.core.remote.WorkspaceListResponse
import com.example.app.feature.pairing.PairingViewModel
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
class PairingViewModelTest {

    @Test
    fun `scan saves credentials and loads workspaces`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore()
        val api = FakeRemoteApi(
            workspaces = listOf(
                Workspace(id = "one", name = "First workspace"),
                Workspace(id = "two", name = "Second workspace"),
            ),
        )
        val viewModel = createViewModel(store, api, dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertFalse(viewModel.uiState.value.isPaired)

        viewModel.onPairingPayload("""{"ticket":"ticket-1","token":"token-1"}""")

        assertTrue(viewModel.uiState.value.isLoading)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PairingCredentials("ticket-1", "token-1"), store.savedCredentials)
        assertTrue(viewModel.uiState.value.isPaired)
        assertFalse(viewModel.uiState.value.isLoading)
        assertEquals(listOf("one", "two"), viewModel.uiState.value.workspaces.map { it.id })
    }

    @Test
    fun `invalid scan shows an error without saving credentials`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore()
        val viewModel = createViewModel(store, FakeRemoteApi(), dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onPairingPayload("not-json")

        assertFalse(viewModel.uiState.value.isLoading)
        assertFalse(viewModel.uiState.value.isPaired)
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("not a valid"))
        assertNull(store.savedCredentials)
    }

    @Test
    fun `stored pairing failure can be retried`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val credentials = PairingCredentials("stored-ticket", "stored-token")
        val store = FakeCredentialStore(credentials)
        val api = FakeRemoteApi(error = IOException("offline"))
        val viewModel = createViewModel(store, api, dispatcher)

        assertTrue(viewModel.uiState.value.isLoading)
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.isPaired)
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("could not be loaded"))

        api.error = null
        api.workspaces = listOf(Workspace(id = "workspace", name = "Workspace"))
        viewModel.onRetry()

        assertTrue(viewModel.uiState.value.isLoading)
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isLoading)
        assertNull(viewModel.uiState.value.errorMessage)
        assertEquals("workspace", viewModel.uiState.value.workspaces.single().id)
        assertEquals(2, api.workspaceRequests)
    }

    @Test
    fun `workspace selection only accepts a loaded workspace`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(PairingCredentials("ticket", "token"))
        val api = FakeRemoteApi(
            workspaces = listOf(Workspace(id = "workspace", name = "Workspace")),
        )
        val viewModel = createViewModel(store, api, dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onWorkspaceSelected("missing")
        assertNull(viewModel.uiState.value.selectedWorkspaceId)

        viewModel.onWorkspaceSelected("workspace")
        assertEquals("workspace", viewModel.uiState.value.selectedWorkspaceId)
    }

    @Test
    fun `empty workspace response exposes a completed paired state`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val credentials = PairingCredentials("ticket", "token")
        val store = FakeCredentialStore(credentials)
        val viewModel = createViewModel(store, FakeRemoteApi(), dispatcher)

        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(viewModel.uiState.value.isPaired)
        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.workspaces.isEmpty())
        assertNull(viewModel.uiState.value.errorMessage)
    }

    @Test
    fun `save failure resets pairing state and exposes an actionable error`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(saveError = IOException("disk full"))
        val viewModel = createViewModel(store, FakeRemoteApi(), dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onPairingPayload("""{"ticket":"ticket-1","token":"token-1"}""")
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isPaired)
        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.workspaces.isEmpty())
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("could not be saved"))
        assertNull(store.savedCredentials)
    }

    @Test
    fun `stored credential read failure exposes a pairing error without calling the api`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(readError = IOException("unreadable"))
        val api = FakeRemoteApi()
        val viewModel = createViewModel(store, api, dispatcher)

        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isPaired)
        assertFalse(viewModel.uiState.value.isLoading)
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("could not be read"))
        assertEquals(0, api.workspaceRequests)
    }

    @Test
    fun `remote factory receives scanned credentials`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore()
        val api = FakeRemoteApi()
        val viewModel = createViewModel(store, api, dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onPairingPayload("""{"ticket":" scanned-ticket ","token":" scanned-token "}""")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PairingCredentials("scanned-ticket", "scanned-token"), api.receivedCredentials)
    }

    private fun createViewModel(
        store: FakeCredentialStore,
        api: FakeRemoteApi,
        dispatcher: CoroutineDispatcher,
    ) = PairingViewModel(
        credentialStore = store,
        remoteApiFactory = { credentials ->
            api.receivedCredentials = credentials
            api
        },
        dispatchers = TestDispatcherProvider(dispatcher),
    )

    private class FakeCredentialStore(
        initialCredentials: PairingCredentials? = null,
        private val readError: Throwable? = null,
        private val saveError: Throwable? = null,
    ) : PairingCredentialStore {
        private val credentialFlow = MutableStateFlow(initialCredentials)
        override val credentials: Flow<PairingCredentials?> =
            if (readError == null) {
                credentialFlow
            } else {
                flow { throw checkNotNull(readError) }
            }
        var savedCredentials: PairingCredentials? = null

        override suspend fun save(credentials: PairingCredentials) {
            saveError?.let { throw it }
            savedCredentials = credentials
            credentialFlow.value = credentials
        }

        override suspend fun clear() {
            savedCredentials = null
            credentialFlow.value = null
        }
    }

    private class FakeRemoteApi(
        var workspaces: List<Workspace> = emptyList(),
        var error: Throwable? = null,
    ) : SidekickRemoteApi {
        var workspaceRequests = 0
        var receivedCredentials: PairingCredentials? = null

        override suspend fun getWorkspaces(): WorkspaceListResponse {
            workspaceRequests += 1
            error?.let { throw it }
            return WorkspaceListResponse(workspaces)
        }

        override suspend fun getTasks(workspaceId: String): TaskListResponse {
            throw UnsupportedOperationException("Tasks are not used by pairing tests")
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