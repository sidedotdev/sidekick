package com.example.app.core.remote

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.test.core.app.ApplicationProvider
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class DataStoreWorkspaceSelectionStoreTest {
    @get:Rule
    val temporaryFolder = TemporaryFolder()

    @Test
    fun `saves and reads back the last workspace id`() = runTest {
        val store = DataStoreWorkspaceSelectionStore(createDataStore(backgroundScope, "round-trip"))

        assertNull(store.lastWorkspaceId.first())

        store.save(" workspace-1 ")

        assertEquals("workspace-1", store.lastWorkspaceId.first())

        store.save("workspace-2")

        assertEquals("workspace-2", store.lastWorkspaceId.first())
    }

    @Test
    fun `clear forgets the last workspace id`() = runTest {
        val store = DataStoreWorkspaceSelectionStore(createDataStore(backgroundScope, "clear"))
        store.save("workspace-1")

        store.clear()

        assertNull(store.lastWorkspaceId.first())
    }

    @Test
    fun `blank ids are rejected without changing the stored value`() = runTest {
        val store = DataStoreWorkspaceSelectionStore(createDataStore(backgroundScope, "blank"))
        store.save("workspace-1")

        val result = runCatching { store.save("   ") }

        assertTrue(result.exceptionOrNull() is IllegalArgumentException)
        assertEquals("workspace-1", store.lastWorkspaceId.first())
    }

    @Test
    fun `context constructor shares the app data store with pairing credentials`() = runTest {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val selectionStore = DataStoreWorkspaceSelectionStore(context)
        val credentialStore = DataStorePairingCredentialStore(context)
        val credentials = PairingCredentials("ticket", "token")

        credentialStore.save(credentials)
        selectionStore.save("workspace-1")

        assertEquals(credentials, credentialStore.credentials.first())
        assertEquals("workspace-1", selectionStore.lastWorkspaceId.first())

        selectionStore.clear()
        credentialStore.clear()
    }

    @Test
    fun `coexists with pairing credentials in the same data store`() = runTest {
        val dataStore = createDataStore(backgroundScope, "shared")
        val selectionStore = DataStoreWorkspaceSelectionStore(dataStore)
        val credentialStore = DataStorePairingCredentialStore(dataStore)
        val credentials = PairingCredentials("ticket", "token")

        credentialStore.save(credentials)
        selectionStore.save("workspace-1")

        assertEquals(credentials, credentialStore.credentials.first())
        assertEquals("workspace-1", selectionStore.lastWorkspaceId.first())

        selectionStore.clear()

        assertEquals(credentials, credentialStore.credentials.first())
        assertNull(selectionStore.lastWorkspaceId.first())

        selectionStore.save("workspace-2")
        credentialStore.clear()

        assertNull(credentialStore.credentials.first())
        assertEquals("workspace-2", selectionStore.lastWorkspaceId.first())
    }

    private fun createDataStore(scope: CoroutineScope, name: String): DataStore<Preferences> =
        PreferenceDataStoreFactory.create(
            scope = scope,
            produceFile = { File(temporaryFolder.root, "$name.preferences_pb") },
        )
}