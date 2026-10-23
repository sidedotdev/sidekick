package com.example.app.core.remote

/**
 * One-line technical summary of a failure and its cause chain, suitable for
 * showing under a user-facing error message and for bug reports: it names the
 * failing stage (via exception types) rather than only the generic outcome.
 */
fun Throwable.describeCauseChain(): String =
    generateSequence(this) { current -> current.cause?.takeIf { it !== current } }
        .joinToString(separator = " <- ") { error ->
            val name = error.javaClass.simpleName.ifEmpty { error.javaClass.name }
            val message = error.message?.takeIf { it.isNotBlank() }
            if (message == null) name else "$name: $message"
        }

/** Replaces every occurrence of a secret so diagnostics can be shared safely. */
fun String.redactSecrets(secrets: Iterable<String>): String =
    secrets.filter { it.isNotEmpty() }.fold(this) { redacted, secret ->
        redacted.replace(secret, "<redacted>")
    }
