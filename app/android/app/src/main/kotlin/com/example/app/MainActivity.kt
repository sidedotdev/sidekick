package com.example.app

import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.pairing.PairingRoute
import com.example.app.feature.tasks.TaskListRoute

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            AppTheme {
                val navController = rememberNavController()
                NavHost(
                    navController = navController,
                    startDestination = PAIRING_ROUTE,
                ) {
                    composable(PAIRING_ROUTE) {
                        PairingRoute(
                            onWorkspaceSelected = { workspace ->
                                navController.navigate(
                                    "$TASKS_ROUTE/${Uri.encode(workspace.id)}",
                                )
                            },
                        )
                    }
                    composable(
                        route = "$TASKS_ROUTE/{workspaceId}",
                        arguments = listOf(
                            navArgument("workspaceId") {
                                type = NavType.StringType
                            },
                        ),
                    ) { backStackEntry ->
                        val workspaceId = requireNotNull(
                            backStackEntry.arguments?.getString("workspaceId"),
                        )
                        TaskListRoute(
                            workspaceId = workspaceId,
                            onBack = navController::popBackStack,
                        )
                    }
                }
            }
        }
    }

    private companion object {
        const val PAIRING_ROUTE = "pairing"
        const val TASKS_ROUTE = "tasks"
    }
}