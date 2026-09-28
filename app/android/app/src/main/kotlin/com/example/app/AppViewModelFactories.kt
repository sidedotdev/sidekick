package com.example.app

import android.content.Context
import androidx.lifecycle.ViewModelProvider
import com.example.app.feature.pairing.PairingViewModelFactory
import com.example.app.feature.tasks.TasksViewModelFactory

/** Seam that lets navigation tests wire fake stores and APIs into the real routes. */
interface AppViewModelFactories {
    fun pairing(restoreStoredPairing: Boolean): ViewModelProvider.Factory
    fun tasks(hintedWorkspaceId: String?): ViewModelProvider.Factory
}

class DataStoreAppViewModelFactories(context: Context) : AppViewModelFactories {
    private val applicationContext = context.applicationContext

    override fun pairing(restoreStoredPairing: Boolean): ViewModelProvider.Factory =
        PairingViewModelFactory(applicationContext, restoreStoredPairing)

    override fun tasks(hintedWorkspaceId: String?): ViewModelProvider.Factory =
        TasksViewModelFactory(applicationContext, hintedWorkspaceId)
}