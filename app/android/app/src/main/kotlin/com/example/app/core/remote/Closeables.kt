package com.example.app.core.remote

internal fun closeQuietly(resource: AutoCloseable?) {
    try {
        resource?.close()
    } catch (_: Exception) {
    }
}