package com.example.app.core.remote

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

@Serializable
data class PairingCredentials(
    val ticket: String,
    val token: String,
)

class PairingPayloadParser(
    private val json: Json = Json {
        ignoreUnknownKeys = true
    },
) {
    fun parse(payload: String): PairingCredentials {
        val credentials = try {
            json.decodeFromString<PairingCredentials>(payload.trim())
        } catch (error: Exception) {
            throw InvalidPairingPayloadException("The QR code is not a Sidekick pairing payload", error)
        }

        if (credentials.ticket.isBlank() || credentials.token.isBlank()) {
            throw InvalidPairingPayloadException("The pairing ticket and token must not be empty")
        }

        return PairingCredentials(
            ticket = credentials.ticket.trim(),
            token = credentials.token.trim(),
        )
    }
}

class InvalidPairingPayloadException(
    message: String,
    cause: Throwable? = null,
) : IllegalArgumentException(message, cause)