package com.example.app.core.remote

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import java.io.IOException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.map

/** Remembers the workspace the user last opened on this device. */
interface WorkspaceSelectionStore {
    val lastWorkspaceId: Flow<String?>

    suspend fun save(id: String)

    suspend fun clear()
}

class DataStoreWorkspaceSelectionStore(
    private val dataStore: DataStore<Preferences>,
) : WorkspaceSelectionStore {
    constructor(context: Context) : this(context.applicationContext.appDataStore)

    override val lastWorkspaceId: Flow<String?> = dataStore.data
        .catch { error ->
            if (error is IOException) {
                emit(emptyPreferences())
            } else {
                throw error
            }
        }
        .map { preferences ->
            preferences[LAST_WORKSPACE_ID_KEY]?.trim()?.takeIf { it.isNotEmpty() }
        }

    override suspend fun save(id: String) {
        val workspaceId = id.trim()
        require(workspaceId.isNotEmpty()) { "Workspace id must not be empty" }

        dataStore.edit { preferences ->
            preferences[LAST_WORKSPACE_ID_KEY] = workspaceId
        }
    }

    override suspend fun clear() {
        dataStore.edit { preferences ->
            preferences.remove(LAST_WORKSPACE_ID_KEY)
        }
    }

    private companion object {
        val LAST_WORKSPACE_ID_KEY = stringPreferencesKey("last_workspace_id")
    }
}