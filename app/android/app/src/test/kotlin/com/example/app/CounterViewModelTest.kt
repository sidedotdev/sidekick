package com.example.app

import app.cash.turbine.test
import com.example.app.core.coroutine.DispatcherProvider
import com.example.app.feature.counter.CounterViewModel
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class CounterViewModelTest {

    private class TestDispatcherProvider(
        private val dispatcher: CoroutineDispatcher,
    ) : DispatcherProvider {
        override val default: CoroutineDispatcher = dispatcher
        override val io: CoroutineDispatcher = dispatcher
        override val main: CoroutineDispatcher = dispatcher
    }

    @Test
    fun `increment updates count`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val viewModel = CounterViewModel(TestDispatcherProvider(dispatcher))

        viewModel.uiState.test {
            assertEquals(0, awaitItem().count)

            viewModel.onIncrement()
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(1, awaitItem().count)

            cancelAndIgnoreRemainingEvents()
        }
    }
}