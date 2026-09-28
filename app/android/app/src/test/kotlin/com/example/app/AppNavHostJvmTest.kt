package com.example.app

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.hasTestTag
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.navigation.NavHostController
import androidx.navigation.compose.rememberNavController
import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.core.remote.PairingCredentialStore
import com.example.app.core.remote.PairingCredentials
import com.example.app.core.remote.SidekickRemoteApi
import com.example.app.core.remote.Task
import com.example.app.core.remote.TaskListResponse
import com.example.app.core.remote.Workspace
import com.example.app.core.remote.WorkspaceListResponse
import com.example.app.core.remote.WorkspaceSelectionStore
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.pairing.PairingViewModel
import com.example.app.feature.tasks.TasksViewModel
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Exercises the real navigation graph with fake storage and API. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h640dp-xhdpi")
class AppNavHostJvmTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val storedCredentials = PairingCredentials("ticket", "token")
    private val workspaces = listOf(Workspace("ws-1", "Alpha"), Workspace("ws-2", "Beta"))
    private val tasks = (1..12).map { index ->
        Task(
            id = "t$index",
            workspaceId = "ws-1",
            title = "Task number $index",
            description = if (index == 7) "needle in the haystack" else "",
            status = "to_do",
            updated = "2026-09-28T10:%02d:00Z".format(index),
        )
    }

    private val credentialStore = FakeCredentialStore()
    private val remoteApi = FakeRemoteApi(workspaces, mapOf("ws-1" to tasks, "ws-2" to emptyList()))
    private val factories = RecordingFactories(credentialStore, remoteApi)
    private lateinit var navController: NavHostController

    private fun launchApp() {
        composeRule.setContent {
            navController = rememberNavController()
            AppTheme(darkTheme = false) {
                AppNavHost(factories = factories, navController = navController)
            }
        }
    }

    @Test
    fun storedCredentialsLandOnTasksWithNothingBehindThem() {
        credentialStore.stored.value = storedCredentials
        launchApp()

        composeRule.onNodeWithTag("workspace-title").assertTextEquals("Alpha")
        composeRule.onNodeWithTag("task-t12").assertIsDisplayed()
        composeRule.onNodeWithTag("scan-pairing-code").assertDoesNotExist()
        composeRule.runOnIdle { assertNull(navController.previousBackStackEntry) }
    }

    @Test
    fun unpairedLaunchStaysOnPairingUntilAScanAndForwardsTheHint() {
        launchApp()

        composeRule.onNodeWithTag("scan-pairing-code").assertIsDisplayed()
        composeRule.runOnIdle {
            factories.pairingViewModels.single().onPairingPayload(
                """{"ticket":"ticket","token":"token","workspaceId":"ws-2"}""",
            )
        }

        composeRule.onNodeWithTag("workspace-title").assertTextEquals("Beta")
        composeRule.runOnIdle { assertNull(navController.previousBackStackEntry) }
    }

    @Test
    fun scanningADifferentCodeStaysOnPairingDespiteStoredCredentials() {
        credentialStore.stored.value = storedCredentials
        launchApp()
        composeRule.onNodeWithTag("workspace-title").assertTextEquals("Alpha")

        composeRule.onNodeWithTag("overflow-menu").performClick()
        composeRule.onNodeWithTag("scan-different-code").performClick()

        composeRule.onNodeWithTag("scan-pairing-code").assertIsDisplayed()
        composeRule.onNodeWithTag("workspace-title").assertDoesNotExist()
        composeRule.runOnIdle {
            assertNull(navController.previousBackStackEntry)
            factories.pairingViewModels.last().onPairingPayload("""{"ticket":"new","token":"new"}""")
        }

        composeRule.onNodeWithTag("workspace-title").assertTextEquals("Alpha")
        composeRule.runOnIdle {
            assertEquals(PairingCredentials("new", "new"), credentialStore.stored.value)
            assertEquals(2, factories.tasksViewModels.size)
        }
    }

    @Test
    fun taskDetailSharesTheListViewModelAndBackRestoresSearchAndScroll() {
        credentialStore.stored.value = storedCredentials
        launchApp()

        composeRule.onNodeWithTag("task-list").performScrollToNode(hasTestTag("task-t1"))
        composeRule.onNodeWithTag("task-t1").assertIsDisplayed().performClick()
        composeRule.onNodeWithTag("detail-title").assertTextEquals("Task number 1")
        composeRule.onNodeWithTag("detail-back").performClick()
        composeRule.onNodeWithTag("task-t1").assertIsDisplayed()

        composeRule.onNodeWithTag("search-open").performClick()
        composeRule.onNodeWithTag("task-search").performTextInput("needle")
        composeRule.onNodeWithTag("task-t7").assertIsDisplayed()
        composeRule.onNodeWithTag("task-t12").assertDoesNotExist()

        composeRule.onNodeWithTag("task-t7").performClick()
        composeRule.onNodeWithTag("detail-title").assertTextEquals("Task number 7")
        composeRule.onNodeWithTag("detail-back").performClick()

        composeRule.onNodeWithTag("task-t7").assertIsDisplayed()
        composeRule.onNodeWithTag("task-t12").assertDoesNotExist()
        composeRule.runOnIdle {
            assertEquals(1, factories.tasksViewModels.size)
            assertEquals(listOf("ws-1"), remoteApi.requestedWorkspaceIds)
        }
    }

    @Test
    fun detailForATaskFromAnotherWorkspaceIsReportedMissing() {
        credentialStore.stored.value = storedCredentials
        launchApp()
        composeRule.onNodeWithTag("task-t12").assertIsDisplayed()

        composeRule.runOnIdle { navController.navigate("tasks/ws-2/t12") }

        composeRule.onNodeWithTag("detail-missing").assertIsDisplayed()
        composeRule.onNodeWithTag("detail-title").assertDoesNotExist()
    }

    private class RecordingFactories(
        private val credentialStore: PairingCredentialStore,
        private val remoteApi: SidekickRemoteApi,
    ) : AppViewModelFactories {
        val pairingViewModels = mutableListOf<PairingViewModel>()
        val tasksViewModels = mutableListOf<TasksViewModel>()
        private val dispatchers = MainDispatcherProvider()

        override fun pairing(restoreStoredPairing: Boolean): ViewModelProvider.Factory = factory {
            PairingViewModel(
                credentialStore = credentialStore,
                restoreStoredPairing = restoreStoredPairing,
                dispatchers = dispatchers,
            ).also { pairingViewModels += it }
        }

        override fun tasks(hintedWorkspaceId: String?): ViewModelProvider.Factory = factory {
            TasksViewModel(
                hintedWorkspaceId = hintedWorkspaceId,
                credentialStore = credentialStore,
                workspaceSelectionStore = FakeWorkspaceSelectionStore(),
                remoteApiFactory = { remoteApi },
                dispatchers = dispatchers,
            ).also { tasksViewModels += it }
        }

        private fun factory(create: () -> ViewModel): ViewModelProvider.Factory =
            object : ViewModelProvider.Factory {
                @Suppress("UNCHECKED_CAST")
                override fun <T : ViewModel> create(modelClass: Class<T>): T = create() as T
            }
    }

    /** Keeps all work on the Robolectric main looper so the compose rule can idle it. */
    private class MainDispatcherProvider : DispatcherProvider {
        override val default: CoroutineDispatcher = Dispatchers.Main
        override val io: CoroutineDispatcher = Dispatchers.Main
        override val main: CoroutineDispatcher = Dispatchers.Main
    }

    private class FakeCredentialStore : PairingCredentialStore {
        val stored = MutableStateFlow<PairingCredentials?>(null)
        override val credentials: Flow<PairingCredentials?> = stored

        override suspend fun save(credentials: PairingCredentials) {
            stored.value = credentials
        }

        override suspend fun clear() {
            stored.value = null
        }
    }

    private class FakeWorkspaceSelectionStore : WorkspaceSelectionStore {
        override val lastWorkspaceId = MutableStateFlow<String?>(null)

        override suspend fun save(id: String) {
            lastWorkspaceId.value = id
        }

        override suspend fun clear() {
            lastWorkspaceId.value = null
        }
    }

    private class FakeRemoteApi(
        private val workspaces: List<Workspace>,
        private val tasksByWorkspace: Map<String, List<Task>>,
    ) : SidekickRemoteApi {
        val requestedWorkspaceIds = mutableListOf<String>()

        override suspend fun getWorkspaces() = WorkspaceListResponse(workspaces)

        override suspend fun getTasks(workspaceId: String): TaskListResponse {
            requestedWorkspaceIds += workspaceId
            return TaskListResponse(tasksByWorkspace[workspaceId].orEmpty())
        }
    }
}