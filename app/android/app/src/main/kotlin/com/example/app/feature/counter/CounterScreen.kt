package com.example.app.feature.counter

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.example.app.core.ui.theme.AppTheme

@Composable
fun CounterRoute(
    modifier: Modifier = Modifier,
    viewModel: CounterViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    CounterScreen(
        state = state,
        onIncrement = viewModel::onIncrement,
        modifier = modifier,
    )
}

@Composable
fun CounterScreen(
    state: CounterUiState,
    onIncrement: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxSize().padding(16.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = state.count.toString(),
            modifier = Modifier.testTag("counter"),
        )
        Button(
            onClick = onIncrement,
            modifier = Modifier.testTag("increment"),
        ) {
            Text(text = "Increment")
        }
    }
}

@Preview
@Composable
private fun CounterScreenPreview() {
    AppTheme {
        CounterScreen(state = CounterUiState(count = 0), onIncrement = {})
    }
}