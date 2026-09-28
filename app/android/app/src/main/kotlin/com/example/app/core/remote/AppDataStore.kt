package com.example.app.core.remote

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.preferencesDataStore

/**
 * Single preferences DataStore shared by all locally persisted app state. The file name
 * predates the store being shared and is kept so already-paired devices keep their credentials.
 */
internal val Context.appDataStore: DataStore<Preferences> by preferencesDataStore(
    name = "remote_pairing",
)