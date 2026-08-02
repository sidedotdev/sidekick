package com.example.app.core.remote

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import java.io.IOException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.map

interface PairingCredentialStore {
    val credentials: Flow<PairingCredentials?>

    suspend fun save(credentials: PairingCredentials)

    suspend fun clear()
}

private val Context.pairingDataStore: DataStore<Preferences> by preferencesDataStore(
    name = "remote_pairing",
)

class DataStorePairingCredentialStore(
    private val dataStore: DataStore<Preferences>,
) : PairingCredentialStore {
    constructor(context: Context) : this(context.applicationContext.pairingDataStore)

    override val credentials: Flow<PairingCredentials?> = dataStore.data
        .catch { error ->
            if (error is IOException) {
                emit(emptyPreferences())
            } else {
                throw error
            }
        }
        .map { preferences ->
            val ticket = preferences[TICKET_KEY]?.trim()
            val token = preferences[TOKEN_KEY]?.trim()
            if (ticket.isNullOrBlank() || token.isNullOrBlank()) {
                null
            } else {
                PairingCredentials(ticket = ticket, token = token)
            }
        }

    override suspend fun save(credentials: PairingCredentials) {
        val ticket = credentials.ticket.trim()
        val token = credentials.token.trim()
        require(ticket.isNotEmpty()) { "Pairing ticket must not be empty" }
        require(token.isNotEmpty()) { "Pairing token must not be empty" }

        dataStore.edit { preferences ->
            preferences[TICKET_KEY] = ticket
            preferences[TOKEN_KEY] = token
        }
    }

    override suspend fun clear() {
        dataStore.edit { preferences ->
            preferences.remove(TICKET_KEY)
            preferences.remove(TOKEN_KEY)
        }
    }

    private companion object {
        val TICKET_KEY = stringPreferencesKey("ticket")
        val TOKEN_KEY = stringPreferencesKey("token")
    }
}