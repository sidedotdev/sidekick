package com.example.app.core.remote

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import java.io.File
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

@OptIn(ExperimentalCoroutinesApi::class)
class PairingCredentialsTest {
    @get:Rule
    val temporaryFolder = TemporaryFolder()

    @Test
    fun `parser accepts pairing response and ignores device metadata`() {
        val payload = """
            {
              "device": {"id": "device-1", "name": "Phone"},
              "ticket": " endpoint-ticket ",
              "token": " bearer-token "
            }
        """.trimIndent()

        assertEquals(
            PairingCredentials("endpoint-ticket", "bearer-token"),
            PairingPayloadParser().parse(payload),
        )
    }

    @Test
    fun `parser rejects malformed incomplete and incorrectly typed payloads`() {
        listOf(
            "not-json",
            " ",
            """{"ticket":"ticket"}""",
            """{"ticket":"","token":"token"}""",
            """{"ticket":"ticket","token":""}""",
            """{"ticket":123,"token":"token"}""",
            """{"ticket":"ticket","token":null}""",
            """{"ticket":["ticket"],"token":"token"}""",
        ).forEach { payload ->
            assertThrows(InvalidPairingPayloadException::class.java) {
                PairingPayloadParser().parse(payload)
            }
        }
    }

    @Test
    fun `data store saves and clears credentials atomically`() = runTest {
        val dataStore = PreferenceDataStoreFactory.create(
            scope = backgroundScope,
            produceFile = {
                File(temporaryFolder.root, "pairing.preferences_pb")
            },
        )
        val store = DataStorePairingCredentialStore(dataStore)
        val credentials = PairingCredentials("ticket", "token")

        assertNull(store.credentials.first())

        store.save(credentials)
        assertEquals(credentials, store.credentials.first())

        store.clear()
        assertNull(store.credentials.first())
    }

    @Test
    fun `data store trims credentials before saving`() = runTest {
        val dataStore = PreferenceDataStoreFactory.create(
            scope = backgroundScope,
            produceFile = {
                File(temporaryFolder.root, "trimmed-pairing.preferences_pb")
            },
        )
        val store = DataStorePairingCredentialStore(dataStore)

        store.save(PairingCredentials(" ticket ", " token "))

        assertEquals(
            PairingCredentials("ticket", "token"),
            store.credentials.first(),
        )
    }

    @Test
    fun `data store treats partial and blank persisted credentials as absent`() = runTest {
        val dataStore = PreferenceDataStoreFactory.create(
            scope = backgroundScope,
            produceFile = {
                File(temporaryFolder.root, "invalid-pairing.preferences_pb")
            },
        )
        val store = DataStorePairingCredentialStore(dataStore)
        val ticketKey = stringPreferencesKey("ticket")
        val tokenKey = stringPreferencesKey("token")

        dataStore.edit { preferences ->
            preferences[ticketKey] = "ticket"
        }
        assertNull(store.credentials.first())

        dataStore.edit { preferences ->
            preferences[tokenKey] = "   "
        }
        assertNull(store.credentials.first())
    }

    @Test
    fun `data store rejects blank credentials without changing stored values`() = runTest {
        val dataStore = PreferenceDataStoreFactory.create(
            scope = backgroundScope,
            produceFile = {
                File(temporaryFolder.root, "blank-pairing.preferences_pb")
            },
        )
        val store = DataStorePairingCredentialStore(dataStore)
        val credentials = PairingCredentials("ticket", "token")
        store.save(credentials)

        assertThrows(IllegalArgumentException::class.java) {
            kotlinx.coroutines.runBlocking {
                store.save(PairingCredentials(" ", "token"))
            }
        }
        assertThrows(IllegalArgumentException::class.java) {
            kotlinx.coroutines.runBlocking {
                store.save(PairingCredentials("ticket", "\t"))
            }
        }

        assertEquals(credentials, store.credentials.first())
    }
}