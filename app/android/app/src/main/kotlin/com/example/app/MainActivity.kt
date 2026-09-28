package com.example.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.runtime.remember
import com.example.app.core.ui.theme.AppTheme
import com.example.app.feature.tasks.DesignVariantsProvider

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            val factories = remember { DataStoreAppViewModelFactories(this) }
            AppTheme {
                DesignVariantsProvider {
                    AppNavHost(factories = factories)
                }
            }
        }
    }
}