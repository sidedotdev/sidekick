package com.example.app.core.remote

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.Interceptor
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import retrofit2.http.GET
import retrofit2.http.Path
import okhttp3.MediaType.Companion.toMediaType

@Serializable
data class Workspace(
    val id: String,
    val name: String,
    val localRepoDir: String = "",
    val configMode: String = "",
    val created: String = "",
    val updated: String = "",
)

@Serializable
data class Task(
    val id: String,
    val workspaceId: String,
    val title: String,
    val description: String = "",
    val status: String,
    val agentType: String = "",
    val flowType: String = "",
    val created: String = "",
    val updated: String = "",
)

@Serializable
data class WorkspaceListResponse(
    val workspaces: List<Workspace>,
)

@Serializable
data class TaskListResponse(
    val tasks: List<Task>,
)

interface SidekickRemoteApi {
    @GET("api/v1/workspaces/")
    suspend fun getWorkspaces(): WorkspaceListResponse

    @GET("api/v1/workspaces/{workspaceId}/tasks/")
    suspend fun getTasks(
        @Path("workspaceId") workspaceId: String,
    ): TaskListResponse
}

class SidekickRemoteApiFactory(
    private val json: Json = Json {
        ignoreUnknownKeys = true
    },
) {
    fun create(
        credentials: PairingCredentials,
        connector: IrohConnector,
    ): SidekickRemoteApi = create(
        token = credentials.token,
        callFactory = IrohHttpCallFactory(credentials.ticket, connector),
    )

    fun create(
        token: String,
        callFactory: Call.Factory,
        baseUrl: String = "http://sidekick/",
    ): SidekickRemoteApi {
        require(token.isNotBlank()) { "Pairing token must not be empty" }

        val authenticatedCalls = Call.Factory { request ->
            callFactory.newCall(
                request.newBuilder()
                    .header("Authorization", "Bearer $token")
                    .build(),
            )
        }

        return Retrofit.Builder()
            .baseUrl(baseUrl)
            .callFactory(authenticatedCalls)
            .addConverterFactory(
                json.asConverterFactory("application/json".toMediaType()),
            )
            .build()
            .create(SidekickRemoteApi::class.java)
    }
}