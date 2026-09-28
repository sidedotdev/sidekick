package com.example.app

import android.net.Uri
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.example.app.feature.pairing.PairingRoute
import com.example.app.feature.pairing.PairingViewModel
import com.example.app.feature.taskdetail.TaskDetailRoute
import com.example.app.feature.tasks.TasksRoute
import com.example.app.feature.tasks.TasksViewModel

private const val PAIRING_ROUTE = "pairing"
private const val RESCAN_ARG = "rescan"
private const val PAIRING_ROUTE_PATTERN = "$PAIRING_ROUTE?$RESCAN_ARG={$RESCAN_ARG}"

private const val HINT_ARG = "hint"
private const val TASKS_GRAPH_ROUTE = "tasks_graph"
private const val TASKS_GRAPH_ROUTE_PATTERN = "$TASKS_GRAPH_ROUTE?$HINT_ARG={$HINT_ARG}"
private const val TASKS_ROUTE = "tasks"
private const val TASK_DETAIL_ROUTE_PATTERN = "$TASKS_ROUTE/{workspaceId}/{taskId}"

@Composable
fun AppNavHost(
    factories: AppViewModelFactories,
    navController: NavHostController = rememberNavController(),
) {
    NavHost(
        navController = navController,
        startDestination = PAIRING_ROUTE_PATTERN,
    ) {
        composable(
            route = PAIRING_ROUTE_PATTERN,
            arguments = listOf(
                navArgument(RESCAN_ARG) {
                    type = NavType.BoolType
                    defaultValue = false
                },
            ),
        ) { backStackEntry ->
            val rescan = backStackEntry.arguments?.getBoolean(RESCAN_ARG) == true
            val factory = remember(backStackEntry) { factories.pairing(restoreStoredPairing = !rescan) }
            val viewModel: PairingViewModel = viewModel(factory = factory)
            PairingRoute(
                viewModel = viewModel,
                onPaired = { hintedWorkspaceId ->
                    navController.navigate(tasksGraphRoute(hintedWorkspaceId)) {
                        popUpTo(PAIRING_ROUTE_PATTERN) { inclusive = true }
                    }
                },
            )
        }
        // The list and detail share one TasksViewModel scoped to this graph, so the graph's
        // lifetime bounds the loaded tasks: re-pairing pops it and starts fresh.
        navigation(
            route = TASKS_GRAPH_ROUTE_PATTERN,
            startDestination = TASKS_ROUTE,
            arguments = listOf(
                navArgument(HINT_ARG) {
                    type = NavType.StringType
                    nullable = true
                },
            ),
        ) {
            composable(route = TASKS_ROUTE) { backStackEntry ->
                TasksRoute(
                    viewModel = backStackEntry.sharedTasksViewModel(navController, factories),
                    onTaskClick = { task ->
                        navController.navigate(
                            "$TASKS_ROUTE/${Uri.encode(task.workspaceId)}/${Uri.encode(task.id)}",
                        )
                    },
                    onScanDifferentCode = { navController.navigateToPairing(rescan = true) },
                    onCredentialsMissing = { navController.navigateToPairing(rescan = false) },
                )
            }
            composable(
                route = TASK_DETAIL_ROUTE_PATTERN,
                arguments = listOf(
                    navArgument("workspaceId") { type = NavType.StringType },
                    navArgument("taskId") { type = NavType.StringType },
                ),
            ) { backStackEntry ->
                val arguments = requireNotNull(backStackEntry.arguments)
                TaskDetailRoute(
                    workspaceId = requireNotNull(arguments.getString("workspaceId")),
                    taskId = requireNotNull(arguments.getString("taskId")),
                    viewModel = backStackEntry.sharedTasksViewModel(navController, factories),
                    onBack = { navController.popBackStack() },
                )
            }
        }
    }
}

private fun tasksGraphRoute(hintedWorkspaceId: String?): String =
    if (hintedWorkspaceId == null) {
        TASKS_GRAPH_ROUTE
    } else {
        "$TASKS_GRAPH_ROUTE?$HINT_ARG=${Uri.encode(hintedWorkspaceId)}"
    }

private fun NavHostController.navigateToPairing(rescan: Boolean) {
    navigate("$PAIRING_ROUTE?$RESCAN_ARG=$rescan") {
        popUpTo(TASKS_GRAPH_ROUTE_PATTERN) { inclusive = true }
    }
}

@Composable
private fun NavBackStackEntry.sharedTasksViewModel(
    navController: NavHostController,
    factories: AppViewModelFactories,
): TasksViewModel {
    val graphEntry = remember(this) { navController.getBackStackEntry(TASKS_GRAPH_ROUTE_PATTERN) }
    val factory = remember(graphEntry) { factories.tasks(graphEntry.arguments?.getString(HINT_ARG)) }
    return viewModel(viewModelStoreOwner = graphEntry, factory = factory)
}