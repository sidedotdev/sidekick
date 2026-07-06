package com.example.app.core.coroutine

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers

/**
 * Abstraction over [CoroutineDispatcher]s so coroutine-driven code can be
 * exercised deterministically in tests by injecting test dispatchers.
 */
interface DispatcherProvider {
    val default: CoroutineDispatcher
    val io: CoroutineDispatcher
    val main: CoroutineDispatcher
}

class DefaultDispatcherProvider : DispatcherProvider {
    override val default: CoroutineDispatcher = Dispatchers.Default
    override val io: CoroutineDispatcher = Dispatchers.IO
    override val main: CoroutineDispatcher = Dispatchers.Main
}