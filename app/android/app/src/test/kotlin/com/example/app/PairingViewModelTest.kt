package com.example.app

import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.feature.pairing.PairingUiState
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
    fun `stored credentials mark the app paired without a workspace hint`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(PairingCredentials("stored-ticket", "stored-token"))
        val viewModel = createViewModel(store, dispatcher)

        assertTrue(viewModel.uiState.value.isCheckingStoredPairing)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            PairingUiState(isCheckingStoredPairing = false, isPaired = true),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `no stored credentials leaves the scanner available`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val viewModel = createViewModel(FakeCredentialStore(), dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PairingUiState(isCheckingStoredPairing = false), viewModel.uiState.value)
    }

    @Test
    fun `scan saves credentials and reports the hinted workspace`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        for (hint in listOf("two", null)) {
            val store = FakeCredentialStore()
            val viewModel = createViewModel(store, dispatcher)
            dispatcher.scheduler.advanceUntilIdle()

            val payload = if (hint == null) {
                """{"ticket":" ticket-1 ","token":" token-1 "}"""
            } else {
                """{"ticket":" ticket-1 ","token":" token-1 ","workspaceId":"$hint"}"""
            }
            viewModel.onPairingPayload(payload)
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(PairingCredentials("ticket-1", "token-1", hint), store.savedCredentials)
            assertEquals(
                PairingUiState(isCheckingStoredPairing = false, isPaired = true, hintedWorkspaceId = hint),
                viewModel.uiState.value,
            )
        }
    }

    @Test
    fun `invalid scan shows an error without saving credentials`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore()
        val viewModel = createViewModel(store, dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onPairingPayload("not-json")

        assertFalse(viewModel.uiState.value.isCheckingStoredPairing)
        assertFalse(viewModel.uiState.value.isPaired)
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("not a valid"))
        assertNull(store.savedCredentials)
    }

    @Test
    fun `save failure keeps the app unpaired and exposes an actionable error`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(saveError = IOException("disk full"))
        val viewModel = createViewModel(store, dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onPairingPayload("""{"ticket":"ticket-1","token":"token-1"}""")
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isPaired)
        assertFalse(viewModel.uiState.value.isCheckingStoredPairing)
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("could not be saved"))
        assertNull(store.savedCredentials)
    }

    @Test
    fun `stored credential read failure exposes a pairing error and a successful scan clears it`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(readError = IOException("unreadable"))
        val viewModel = createViewModel(store, dispatcher)
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(viewModel.uiState.value.isPaired)
        assertFalse(viewModel.uiState.value.isCheckingStoredPairing)
        assertTrue(viewModel.uiState.value.errorMessage!!.contains("could not be read"))

        viewModel.onPairingPayload("""{"ticket":"ticket-1","token":"token-1"}""")
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(viewModel.uiState.value.isPaired)
        assertNull(viewModel.uiState.value.errorMessage)
    }

    @Test
    fun `scanning a different code ignores stored credentials until a new code is scanned`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val store = FakeCredentialStore(PairingCredentials("stored-ticket", "stored-token"))
        val viewModel = createViewModel(store, dispatcher, restoreStoredPairing = false)

        assertEquals(PairingUiState(isCheckingStoredPairing = false), viewModel.uiState.value)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(PairingUiState(isCheckingStoredPairing = false), viewModel.uiState.value)

        viewModel.onPairingPayload("""{"ticket":"new-ticket","token":"new-token"}""")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PairingCredentials("new-ticket", "new-token"), store.savedCredentials)
        assertTrue(viewModel.uiState.value.isPaired)
    }

    private fun createViewModel(
        store: FakeCredentialStore,
        dispatcher: CoroutineDispatcher,
        restoreStoredPairing: Boolean = true,
    ) = PairingViewModel(
        credentialStore = store,
        restoreStoredPairing = restoreStoredPairing,
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

    private class TestDispatcherProvider(
        dispatcher: CoroutineDispatcher,
    ) : DispatcherProvider {
        override val default: CoroutineDispatcher = dispatcher
        override val io: CoroutineDispatcher = dispatcher
        override val main: CoroutineDispatcher = dispatcher
    }
}